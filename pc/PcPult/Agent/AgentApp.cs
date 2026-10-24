using System.IO.Pipes;
using System.Text.Json.Nodes;
using System.Windows.Forms;
using PcPult.Shared;

namespace PcPult.Agent;

public sealed class AgentApp : ApplicationContext
{
    private readonly Control _ui = new();
    private readonly CancellationTokenSource _cts = new();

    public static void Run()
    {
        using var mutex = new Mutex(true, @"Local\PcPultAgent", out var first);
        if (!first)
            return;
        Application.SetHighDpiMode(HighDpiMode.PerMonitorV2);
        Application.EnableVisualStyles();
        Application.SetCompatibleTextRenderingDefault(false);
        Application.Run(new AgentApp());
    }

    private AgentApp()
    {
        _ = _ui.Handle;
        _ = Task.Run(Connect);
    }

    private async Task Connect()
    {
        var failures = 0;
        while (!_cts.IsCancellationRequested)
        {
            try
            {
                await using var pipe = new NamedPipeClientStream(".", PipeChannel.Name, PipeDirection.InOut, PipeOptions.Asynchronous);
                await pipe.ConnectAsync(5000, _cts.Token);
                failures = 0;
                var ch = new PipeChannel(pipe) { OnRequest = Handle };
                await ch.Event("hello", new JsonObject
                {
                    ["user"] = Environment.UserName,
                    ["session"] = System.Diagnostics.Process.GetCurrentProcess().SessionId,
                });
                Log.Info("connected to service");
                await ch.RunAsync(_cts.Token);
                Log.Info("service disconnected");
            }
            catch (Exception ex) when (ex is TimeoutException or IOException or OperationCanceledException)
            {
                failures++;
            }
            catch (Exception ex)
            {
                Log.Error("agent connect", ex);
                failures++;
            }
            if (failures >= 30)
            {
                Log.Info("service is gone, exiting");
                Exit();
                return;
            }
            await Task.Delay(2000);
        }
    }

    private void Exit()
    {
        _cts.Cancel();
        _ui.BeginInvoke(() => ExitThread());
    }

    private async Task<JsonObject> Handle(string op, JsonObject a)
    {
        switch (op)
        {
            case "ping":
                return new JsonObject { ["ok"] = true };
            case "lock":
                if (!Native.LockWorkStation())
                    throw new InvalidOperationException("Windows не дала заблокировать экран");
                return [];
            case "monitor_off":
                Native.PostMessage(new IntPtr(0xFFFF), 0x0112, new IntPtr(0xF170), new IntPtr(2));
                return [];
            case "info":
                return Info();
            case "screenshot":
                return Screens.Capture(a);
            case "screens":
                return Screens.List();
            case "volume":
                return Audio.Volume(a);
            case "media":
                return await MediaControl.Run(a);
            case "popup":
                return OnUi(() => Popup.Show(a));
            case "speak":
                return await Speech.Say(a);
            case "clipboard_get":
                return OnUi(Clip.Get);
            case "clipboard_set":
                return OnUi(() => Clip.Set(a));
            case "launch":
                return Launcher.Start(a);
            case "apps_search":
                return await Launcher.SearchApps(a);
            case "folders":
                return Folders();
            default:
                throw new InvalidOperationException("Неизвестная команда помощника: " + op);
        }
    }

    private static JsonObject Folders()
    {
        var known = new (string Key, string Id)[]
        {
            ("desktop", "B4BFCC3A-DB2C-424C-B029-7FE99A87C641"),
            ("downloads", "374DE290-123F-4565-9164-39C4925E467B"),
            ("documents", "FDD39AD0-238F-46AF-ADB4-6C85480369C7"),
            ("pictures", "33E28130-4E1E-4676-835A-98395C3BC3BB"),
            ("videos", "18989B1D-99B5-455B-841C-AB7C74E4DDFC"),
            ("music", "4BD8D571-6D19-48D3-BE97-422220080E43"),
        };
        var o = new JsonObject();
        foreach (var (key, id) in known)
        {
            var path = Native.KnownFolder(Guid.Parse(id));
            if (!string.IsNullOrEmpty(path))
                o[key] = path;
        }
        return o;
    }

    private JsonObject OnUi(Func<JsonObject> fn)
    {
        JsonObject? result = null;
        Exception? error = null;
        _ui.Invoke(() =>
        {
            try
            {
                result = fn();
            }
            catch (Exception ex)
            {
                error = ex;
            }
        });
        if (error != null)
            throw new InvalidOperationException(error.Message, error);
        return result ?? [];
    }

    private static JsonObject Info()
    {
        var o = new JsonObject
        {
            ["idle_sec"] = (int)Native.IdleTime().TotalSeconds,
            ["locked"] = Native.IsInputDesktopLocked(),
        };
        var hwnd = Native.GetForegroundWindow();
        if (hwnd != IntPtr.Zero)
        {
            o["window"] = Native.TextOf(hwnd);
            Native.GetWindowThreadProcessId(hwnd, out var pid);
            try
            {
                o["process"] = System.Diagnostics.Process.GetProcessById(pid).ProcessName;
            }
            catch
            {
            }
        }
        return o;
    }
}
