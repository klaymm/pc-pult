using System.Net.Http.Json;
using System.Text.Json;
using System.Text.Json.Nodes;
using PcPult.Shared;

namespace PcPult.Service;

public sealed class UserInfo
{
    public long Id { get; set; }
    public string Name { get; set; } = "";
    public string Role { get; set; } = "";
    public List<string> Perms { get; set; } = [];
    public bool Notify { get; set; }
    public bool Owner { get; set; }

    public bool Has(string perm) => Owner || Perms.Contains(perm);
}

public sealed class PollItem
{
    public JsonElement Update { get; set; }
    public UserInfo User { get; set; } = new();
    public int Panel { get; set; }
}

public sealed class RouterClient
{
    private static readonly JsonSerializerOptions Json = new() { PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower };
    private readonly Config _cfg;
    private readonly HttpClient _http;

    public string Session { get; } = Guid.NewGuid().ToString("N")[..8];

    public RouterClient(Config cfg)
    {
        _cfg = cfg;
        _http = new HttpClient { BaseAddress = new Uri(cfg.RouterUrl.TrimEnd('/') + "/"), Timeout = TimeSpan.FromSeconds(60) };
        _http.DefaultRequestHeaders.Add("X-PcPult-Key", cfg.Key);
    }

    public async Task<bool> Hello(bool fresh, CancellationToken ct)
    {
        var body = new JsonObject
        {
            ["version"] = typeof(RouterClient).Assembly.GetName().Version?.ToString(),
            ["host"] = Environment.MachineName,
            ["fresh"] = fresh,
            ["session"] = Session,
        };
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        cts.CancelAfter(TimeSpan.FromSeconds(4));
        using var resp = await _http.PostAsJsonAsync("pc/hello", body, cts.Token);
        return resp.IsSuccessStatusCode;
    }

    public async Task Bye(string reason, long by = 0)
    {
        try
        {
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(8));
            using var _ = await _http.PostAsJsonAsync("pc/bye", new JsonObject { ["reason"] = reason, ["by"] = by }, cts.Token);
        }
        catch
        {
        }
    }

    public async Task<List<PollItem>> Poll(CancellationToken ct)
    {
        using var resp = await _http.GetAsync("pc/poll?timeout=25", HttpCompletionOption.ResponseHeadersRead, ct);
        if (resp.StatusCode == System.Net.HttpStatusCode.Conflict)
            throw new HelloRequiredException();
        resp.EnsureSuccessStatusCode();
        var doc = await resp.Content.ReadFromJsonAsync<PollResponse>(Json, ct);
        return doc?.Items ?? [];
    }

    public async Task<List<long>> Recipients(string perm, CancellationToken ct)
    {
        try
        {
            var doc = await _http.GetFromJsonAsync<RecipientsResponse>($"pc/recipients?perm={perm}", Json, ct);
            return doc?.Ids ?? [];
        }
        catch
        {
            return [];
        }
    }

    public async Task ReportPanel(long chatId, int msgId)
    {
        try
        {
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(8));
            using var _ = await _http.PostAsJsonAsync("pc/panel", new JsonObject { ["chat_id"] = chatId, ["msg_id"] = msgId }, cts.Token);
        }
        catch
        {
        }
    }

    public async Task Audit(long userId, string action)
    {
        try
        {
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(8));
            using var _ = await _http.PostAsJsonAsync("pc/audit", new JsonObject { ["user_id"] = userId, ["action"] = action }, cts.Token);
        }
        catch
        {
        }
    }

    private sealed class PollResponse
    {
        public List<PollItem> Items { get; set; } = [];
    }

    private sealed class RecipientsResponse
    {
        public List<long> Ids { get; set; } = [];
    }
}

public sealed class HelloRequiredException : Exception;
