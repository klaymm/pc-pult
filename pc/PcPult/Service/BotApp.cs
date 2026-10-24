using System.Diagnostics;
using Telegram.Bot;
using PcPult.Features;
using PcPult.Shared;

namespace PcPult.Service;

public sealed class BotApp
{
    private const string DummyToken = "1:pc";

    public Config Cfg { get; }
    public RouterClient Router { get; }
    public ITelegramBotClient Bot { get; }
    public AgentHost Agent { get; }
    public StatusService Status { get; }
    public Pending Pending { get; } = new();
    public PathIds Paths { get; } = new();

    private readonly System.Collections.Concurrent.ConcurrentDictionary<long, int> _nav = new();

    public int Nav(long userId) => _nav.GetValueOrDefault(userId);

    public void Navigated(long userId) => _nav.AddOrUpdate(userId, 1, (_, v) => v + 1);
    public DateTime StartedAt { get; } = DateTime.Now;

    private readonly Lock _byeLock = new();
    private DateTime _byeAt = DateTime.MinValue;

    public bool Quiet { get; private set; }
    public bool ByeSent => Quiet;
    public DateTime ByeAt => _byeAt;

    public BotApp(Config cfg)
    {
        Cfg = cfg;
        Router = new RouterClient(cfg);
        var http = new HttpClient { Timeout = TimeSpan.FromMinutes(10) };
        http.DefaultRequestHeaders.Add("X-PcPult-Key", cfg.Key);
        Bot = new TelegramBotClient(new TelegramBotClientOptions(DummyToken, cfg.RouterUrl.TrimEnd('/')), http);
        Agent = new AgentHost(this);
        Status = new StatusService(cfg);
    }

    public async Task Bye(string reason, long by = 0)
    {
        lock (_byeLock)
        {
            if (Quiet)
                return;
            Quiet = true;
            _byeAt = DateTime.UtcNow;
        }
        Log.Info($"bye: {reason} by {by}");
        await Router.Bye(reason, by);
    }

    public void Resume()
    {
        lock (_byeLock)
            Quiet = false;
    }

    public volatile bool Connected;

    private readonly System.Collections.Concurrent.ConcurrentDictionary<long, int> _alerts = new();

    public async Task Alert(string perm, string text, TimeSpan ttl, CancellationToken ct = default)
    {
        var menu = new Telegram.Bot.Types.ReplyMarkups.InlineKeyboardMarkup(new[] { Ui.BackMenu });
        foreach (var id in await Router.Recipients(perm, ct))
        {
            try
            {
                var sent = await Bot.SendMessage(id, text, Telegram.Bot.Types.Enums.ParseMode.Html, replyMarkup: menu, cancellationToken: ct);
                if (_alerts.TryGetValue(id, out var old))
                    await TryDelete(id, old, ct);
                _alerts[id] = sent.Id;
                _ = ExpireAlert(id, sent.Id, ttl, ct);
            }
            catch (Exception ex)
            {
                Log.Warn($"alert {id}: {ex.Message}");
            }
        }
    }

    private async Task ExpireAlert(long chatId, int msgId, TimeSpan ttl, CancellationToken ct)
    {
        try
        {
            await Task.Delay(ttl, ct);
        }
        catch (OperationCanceledException)
        {
            return;
        }
        if (_alerts.TryGetValue(chatId, out var current) && current == msgId)
        {
            _alerts.TryRemove(chatId, out _);
            await TryDelete(chatId, msgId, ct);
        }
    }

    private async Task TryDelete(long chatId, int msgId, CancellationToken ct)
    {
        try
        {
            await Bot.DeleteMessage(chatId, msgId, ct);
        }
        catch
        {
        }
    }

    public static TimeSpan Uptime => TimeSpan.FromMilliseconds(Environment.TickCount64);

    public static async Task<(int code, string output)> RunProcess(string file, string args, int timeoutSec = 20)
    {
        var psi = new ProcessStartInfo(file, args)
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
            StandardOutputEncoding = System.Text.Encoding.UTF8,
        };
        using var p = Process.Start(psi) ?? throw new InvalidOperationException("process did not start");
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(timeoutSec));
        var stdout = p.StandardOutput.ReadToEndAsync(cts.Token);
        var stderr = p.StandardError.ReadToEndAsync(cts.Token);
        try
        {
            await p.WaitForExitAsync(cts.Token);
        }
        catch (OperationCanceledException)
        {
            try
            {
                p.Kill(true);
            }
            catch
            {
            }
            return (-1, "timeout");
        }
        return (p.ExitCode, (await stdout + await stderr).Trim());
    }
}
