using System.Net.NetworkInformation;
using System.Text;
using LibreHardwareMonitor.Hardware;
using PcPult.Shared;

namespace PcPult.Features;

public sealed record GpuInfo(string Name, int Load, int Temp, double MemUsedMb, double MemTotalMb, double PowerW, int ClockMhz);

public sealed record DiskInfo(string Name, string Label, double Total, double Free);

public sealed record Snapshot(
    double CpuLoad, float? CpuTemp, float? CpuPower, double MemTotal, double MemUsed,
    GpuInfo? Gpu, List<DiskInfo> Disks, double NetDown, double NetUp, TimeSpan Uptime);

public sealed class StatusService : IDisposable
{
    private readonly Config _cfg;
    private readonly Lock _lock = new();
    private volatile Computer? _hw;
    private long _idle, _kernel, _user;
    private double _cpuLoad;
    private long _netRx, _netTx;
    private DateTime _netAt = DateTime.MinValue;
    private double _down, _up;
    private Timer? _timer;

    public StatusService(Config cfg) => _cfg = cfg;

    public void Start()
    {
        Native.GetSystemTimes(out _idle, out _kernel, out _user);
        _timer = new Timer(_ => Sample(), null, TimeSpan.FromSeconds(1), TimeSpan.FromSeconds(2));
        _ = Task.Run(OpenSensors);
    }

    private void OpenSensors()
    {
        try
        {
            var hw = new Computer { IsCpuEnabled = true };
            hw.Open();
            _hw = hw;
        }
        catch (Exception ex)
        {
            Log.Warn("sensors unavailable: " + ex.Message);
        }
    }

    private void Sample()
    {
        try
        {
            lock (_lock)
            {
                if (Native.GetSystemTimes(out var idle, out var kernel, out var user))
                {
                    var di = idle - _idle;
                    var dt = kernel - _kernel + (user - _user);
                    if (dt > 0)
                        _cpuLoad = Math.Clamp(100.0 * (dt - di) / dt, 0, 100);
                    _idle = idle;
                    _kernel = kernel;
                    _user = user;
                }
                long rx = 0, tx = 0;
                foreach (var nic in NetworkInterface.GetAllNetworkInterfaces())
                {
                    if (nic.OperationalStatus != OperationalStatus.Up)
                        continue;
                    if (nic.NetworkInterfaceType is not (NetworkInterfaceType.Ethernet or NetworkInterfaceType.Wireless80211 or NetworkInterfaceType.GigabitEthernet))
                        continue;
                    var d = nic.Description;
                    if (d.Contains("Virtual", StringComparison.OrdinalIgnoreCase) || d.Contains("VPN", StringComparison.OrdinalIgnoreCase) || d.Contains("Tunnel", StringComparison.OrdinalIgnoreCase))
                        continue;
                    var s = nic.GetIPv4Statistics();
                    rx += s.BytesReceived;
                    tx += s.BytesSent;
                }
                var now = DateTime.UtcNow;
                if (_netAt != DateTime.MinValue)
                {
                    var sec = (now - _netAt).TotalSeconds;
                    if (sec > 0 && rx >= _netRx && tx >= _netTx)
                    {
                        _down = (rx - _netRx) / sec;
                        _up = (tx - _netTx) / sec;
                    }
                }
                _netRx = rx;
                _netTx = tx;
                _netAt = now;
            }
        }
        catch (Exception ex)
        {
            Log.Warn("sample: " + ex.Message);
        }
    }

    public (float? temp, float? power) ReadCpu()
    {
        if (_hw == null)
            return (null, null);
        lock (_lock)
        {
            try
            {
                float? temp = null, power = null;
                foreach (var hw in _hw.Hardware.Where(h => h.HardwareType == HardwareType.Cpu))
                {
                    hw.Update();
                    var temps = hw.Sensors.Where(s => s.SensorType == SensorType.Temperature && s.Value.HasValue).ToList();
                    var main = temps.FirstOrDefault(s => s.Name.Contains("Tctl", StringComparison.OrdinalIgnoreCase) || s.Name.Contains("Package", StringComparison.OrdinalIgnoreCase));
                    temp = main?.Value ?? (temps.Count > 0 ? temps.Max(s => s.Value) : null);
                    power = hw.Sensors.FirstOrDefault(s => s.SensorType == SensorType.Power && s.Name.Contains("Package", StringComparison.OrdinalIgnoreCase))?.Value;
                }
                return (temp, power);
            }
            catch (Exception ex)
            {
                Log.Warn("cpu sensors: " + ex.Message);
                return (null, null);
            }
        }
    }

    public async Task<GpuInfo?> ReadGpu()
    {
        try
        {
            var (code, output) = await Service.BotApp.RunProcess("nvidia-smi.exe",
                "--query-gpu=name,utilization.gpu,temperature.gpu,memory.used,memory.total,power.draw,clocks.gr --format=csv,noheader,nounits", 8);
            if (code != 0)
                return null;
            var p = output.Split('\n')[0].Split(',', StringSplitOptions.TrimEntries);
            if (p.Length < 7)
                return null;
            double D(string s) => double.TryParse(s, System.Globalization.CultureInfo.InvariantCulture, out var v) ? v : 0;
            return new GpuInfo(p[0], (int)D(p[1]), (int)D(p[2]), D(p[3]), D(p[4]), D(p[5]), (int)D(p[6]));
        }
        catch
        {
            return null;
        }
    }

    public async Task<Snapshot> Take()
    {
        var gpuTask = ReadGpu();
        var (temp, power) = ReadCpu();
        var (total, avail) = Native.Memory();
        double load, down, up;
        lock (_lock)
        {
            load = _cpuLoad;
            down = _down;
            up = _up;
        }
        var disks = DriveInfo.GetDrives()
            .Where(d => d.DriveType == DriveType.Fixed && d.IsReady)
            .Select(d => new DiskInfo(d.Name.TrimEnd('\\'), d.VolumeLabel, d.TotalSize, d.AvailableFreeSpace))
            .ToList();
        return new Snapshot(load, temp, power, total, total - avail, await gpuTask, disks, down, up, Service.BotApp.Uptime);
    }

    public static string Text(Snapshot s, string pcName, string? userLine)
    {
        var sb = new StringBuilder();
        sb.AppendLine($"🟢 <b>{Fmt.H(pcName)}</b> · работает {Fmt.Dur(s.Uptime)}");
        if (userLine != null)
            sb.AppendLine(userLine);
        sb.AppendLine();
        var cpu = $"🧠 <b>CPU</b> {s.CpuLoad:0}%";
        if (s.CpuTemp is { } t)
            cpu += $" · {t:0}°C";
        if (s.CpuPower is { } w)
            cpu += $" · {w:0} Вт";
        sb.AppendLine(cpu);
        if (s.Gpu is { } g)
        {
            sb.AppendLine($"🎮 <b>GPU</b> {g.Load}% · {g.Temp}°C · {g.PowerW:0} Вт · {g.ClockMhz} МГц");
            sb.AppendLine($"     видеопамять {Fmt.Num(g.MemUsedMb / 1024)} из {Fmt.Num(g.MemTotalMb / 1024)} ГБ");
        }
        var pct = s.MemTotal > 0 ? 100.0 * s.MemUsed / s.MemTotal : 0;
        sb.AppendLine($"💾 <b>ОЗУ</b> {Fmt.Gb(s.MemUsed)} из {Fmt.Gb(s.MemTotal)} ГБ ({pct:0}%)");
        foreach (var d in s.Disks)
            sb.AppendLine($"💽 <b>{Fmt.H(d.Name)}</b> свободно {Fmt.Size(d.Free)} из {Fmt.Size(d.Total)}");
        sb.AppendLine($"🌐 ↓ {Fmt.Size(s.NetDown)}/с · ↑ {Fmt.Size(s.NetUp)}/с");
        return sb.ToString().TrimEnd();
    }

    public void Dispose()
    {
        _timer?.Dispose();
        _hw?.Close();
    }
}
