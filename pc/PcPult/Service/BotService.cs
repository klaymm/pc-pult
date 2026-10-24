using System.Diagnostics;
using System.Runtime.InteropServices;
using Microsoft.Extensions.Hosting;
using PcPult.Shared;

namespace PcPult.Service;

public sealed class BotService : BackgroundService
{
    [DllImport("user32.dll")]
    private static extern int GetSystemMetrics(int index);

    private const int SmShuttingDown = 0x2000;

    private BotApp? _app;

    protected override async Task ExecuteAsync(CancellationToken ct)
    {
        await Task.Yield();
        var clock = Stopwatch.StartNew();
        Log.Init("service");
        var cfg = Config.Load();
        if (string.IsNullOrWhiteSpace(cfg.Key))
        {
            Log.Error("config.json: key is empty, nothing to do");
            return;
        }
        _app = new BotApp(cfg);
        _app.Status.Start();
        _app.Agent.Start();
        _ = Task.Run(() => new Features.Watcher(_app).Run(ct), ct);
        var dispatcher = new Dispatcher(_app);
        Log.Info($"started, windows uptime {Fmt.Dur(BotApp.Uptime)}, router {cfg.RouterUrl}");

        var connected = false;
        var offlineLogged = false;
        var retry = Backoff.First;
        var fresh = BotApp.Uptime < TimeSpan.FromMinutes(15);
        var lastTick = DateTime.UtcNow;
        while (!ct.IsCancellationRequested)
        {
            try
            {
                if (_app.Quiet)
                {
                    await Task.Delay(1000, ct);
                    var now = DateTime.UtcNow;
                    var jumped = now - lastTick > TimeSpan.FromSeconds(8);
                    lastTick = now;
                    if (jumped || now - _app.ByeAt > TimeSpan.FromMinutes(2))
                    {
                        _app.Resume();
                        connected = false;
                        fresh = jumped;
                        retry = Backoff.First;
                        Log.Info(jumped ? "resumed from sleep" : "bye timed out, reconnecting");
                    }
                    continue;
                }
                lastTick = DateTime.UtcNow;
                if (!connected)
                {
                    if (await _app.Router.Hello(fresh, ct))
                    {
                        connected = true;
                        offlineLogged = false;
                        _app.Connected = true;
                        fresh = false;
                        retry = Backoff.First;
                        Log.Info($"connected to router, {clock.Elapsed.TotalSeconds:0.0} s after start");
                    }
                    else
                    {
                        await Task.Delay(retry, ct);
                        retry = Backoff.Next(retry);
                        continue;
                    }
                }
                var items = await _app.Router.Poll(ct);
                foreach (var item in items)
                    _ = Task.Run(() => dispatcher.Handle(item, ct), ct);
            }
            catch (HelloRequiredException)
            {
                connected = false;
                _app.Connected = false;
                retry = Backoff.First;
            }
            catch (OperationCanceledException) when (ct.IsCancellationRequested)
            {
                break;
            }
            catch (Exception ex)
            {
                if (connected)
                    Log.Warn("router link lost: " + ex.Message);
                else if (!offlineLogged)
                    Log.Warn("router is not reachable yet: " + ex.Message);
                offlineLogged = !connected || offlineLogged;
                connected = false;
                _app.Connected = false;
                try
                {
                    await Task.Delay(retry, ct);
                    retry = Backoff.Next(retry);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
            }
        }
    }

    public override async Task StopAsync(CancellationToken cancellationToken)
    {
        await base.StopAsync(cancellationToken);
        if (_app is { Quiet: false })
        {
            var reason = GetSystemMetrics(SmShuttingDown) != 0 ? "shutdown" : "stop";
            await _app.Bye(reason);
        }
        _app?.Agent.Dispose();
        _app?.Status.Dispose();
        Log.Info("stopped");
    }
}
