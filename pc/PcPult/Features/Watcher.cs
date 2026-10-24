using PcPult.Service;
using PcPult.Shared;

namespace PcPult.Features;

public sealed class Watcher
{
    private static readonly TimeSpan AlertTtl = TimeSpan.FromMinutes(30);
    private static readonly TimeSpan DiskRepeat = TimeSpan.FromHours(24);

    private readonly BotApp _app;
    private string? _user;
    private bool _userKnown;
    private bool? _locked;
    private bool _cpuAlerted, _gpuAlerted;
    private int _cpuHot, _gpuHot;
    private readonly Dictionary<string, DateTime> _diskAlerts = [];

    public Watcher(BotApp app) => _app = app;

    public async Task Run(CancellationToken ct)
    {
        var tick = 0;
        while (!ct.IsCancellationRequested)
        {
            try
            {
                await Task.Delay(5000, ct);
                if (_app.Quiet || !_app.Connected)
                    continue;
                await Session(ct);
                if (++tick % 6 == 0)
                    await Hardware(ct);
            }
            catch (OperationCanceledException)
            {
                break;
            }
            catch (Exception ex)
            {
                Log.Warn("watcher: " + ex.Message);
            }
        }
    }

    private static string? ActiveUser()
    {
        var session = Native.WTSGetActiveConsoleSessionId();
        return session == 0xFFFFFFFF ? null : Native.SessionUser(session);
    }

    private async Task Session(CancellationToken ct)
    {
        var user = ActiveUser();
        if (!_userKnown)
        {
            _user = user;
            _userKnown = true;
        }
        else if (user != _user)
        {
            var prev = _user;
            _user = user;
            _locked = null;
            if (user != null)
                await _app.Alert("power", $"👤 <b>{Fmt.H(user)}</b> вошёл в Windows на {Fmt.H(_app.Cfg.PcName)}", AlertTtl, ct);
            else if (prev != null)
                await _app.Alert("power", $"🚪 <b>{Fmt.H(prev)}</b> вышел из Windows на {Fmt.H(_app.Cfg.PcName)}", AlertTtl, ct);
        }
        if (user == null || !_app.Agent.Available)
            return;
        try
        {
            var info = await _app.Agent.Call("info", null, 5, ct);
            var locked = info["locked"]!.GetValue<bool>();
            if (_locked != null && _locked != locked)
                await _app.Alert("power", locked ? $"🔒 {Fmt.H(_app.Cfg.PcName)} заблокирован" : $"🔓 {Fmt.H(_app.Cfg.PcName)} разблокирован", AlertTtl, ct);
            _locked = locked;
        }
        catch (AgentException)
        {
        }
    }

    private async Task Hardware(CancellationToken ct)
    {
        var snap = await _app.Status.Take();
        var cfg = _app.Cfg;

        if (snap.CpuTemp is { } cpu)
            await Heat("CPU", cpu, cfg.CpuTempAlert, () => _cpuHot, v => _cpuHot = v, () => _cpuAlerted, v => _cpuAlerted = v, ct);
        if (snap.Gpu is { } gpu)
            await Heat("GPU", gpu.Temp, cfg.GpuTempAlert, () => _gpuHot, v => _gpuHot = v, () => _gpuAlerted, v => _gpuAlerted = v, ct);

        foreach (var d in snap.Disks)
        {
            var freeGb = d.Free / (1024.0 * 1024 * 1024);
            if (freeGb >= cfg.DiskFreeAlertGb)
            {
                _diskAlerts.Remove(d.Name);
                continue;
            }
            if (_diskAlerts.TryGetValue(d.Name, out var last) && DateTime.UtcNow - last < DiskRepeat)
                continue;
            _diskAlerts[d.Name] = DateTime.UtcNow;
            await _app.Alert("status", $"💽 <b>Мало места на диске {Fmt.H(d.Name)}</b>\nСвободно {Fmt.Size(d.Free)} из {Fmt.Size(d.Total)}", AlertTtl, ct);
        }
    }

    private async Task Heat(string name, double temp, int limit, Func<int> getHot, Action<int> setHot, Func<bool> getAlerted, Action<bool> setAlerted, CancellationToken ct)
    {
        if (limit <= 0)
            return;
        if (temp >= limit)
        {
            setHot(getHot() + 1);
            if (getHot() >= 2 && !getAlerted())
            {
                setAlerted(true);
                await _app.Alert("status", $"🔥 <b>{name} перегревается</b>: {temp:0}°C (порог {limit}°C)\nЕсли ПК под нагрузкой, проверьте охлаждение.", AlertTtl, ct);
            }
        }
        else
        {
            setHot(0);
            if (temp < limit - 5)
                setAlerted(false);
        }
    }
}
