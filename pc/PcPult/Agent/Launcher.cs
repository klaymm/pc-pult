using System.Diagnostics;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace PcPult.Agent;

public static class Launcher
{
    private static readonly Lock CacheLock = new();
    private static List<(string Name, string AppId)> _cache = [];
    private static DateTime _cacheAt = DateTime.MinValue;

    public static async Task<JsonObject> SearchApps(JsonObject a)
    {
        var terms = a["terms"]?.AsArray().Select(t => t?.GetValue<string>() ?? "").Where(t => t.Length > 0).ToList() ?? [];
        var apps = await LoadApps();
        var found = apps
            .Where(x => terms.Any(t => x.Name.Contains(t, StringComparison.OrdinalIgnoreCase)))
            .Where(x => !x.Name.Contains("uninstall", StringComparison.OrdinalIgnoreCase))
            .OrderBy(x => terms.Any(t => x.Name.StartsWith(t, StringComparison.OrdinalIgnoreCase)) ? 0 : 1)
            .ThenBy(x => x.Name.Length)
            .Take(8);
        var arr = new JsonArray();
        foreach (var (name, appId) in found)
            arr.Add(new JsonObject { ["name"] = name, ["app_id"] = appId });
        return new JsonObject { ["apps"] = arr };
    }

    private static async Task<List<(string Name, string AppId)>> LoadApps()
    {
        lock (CacheLock)
        {
            if (_cache.Count > 0 && DateTime.UtcNow - _cacheAt < TimeSpan.FromMinutes(5))
                return _cache;
        }
        var psi = new ProcessStartInfo("powershell.exe",
            "-NoLogo -NoProfile -NonInteractive -ExecutionPolicy Bypass -Command \"[Console]::OutputEncoding=[Text.Encoding]::UTF8; Get-StartApps | ConvertTo-Json -Compress\"")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardOutput = true,
            StandardOutputEncoding = Encoding.UTF8,
        };
        using var p = Process.Start(psi) ?? throw new InvalidOperationException("PowerShell не запустился");
        using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(30));
        var text = await p.StandardOutput.ReadToEndAsync(cts.Token);
        await p.WaitForExitAsync(cts.Token);
        var list = new List<(string, string)>();
        using var doc = JsonDocument.Parse(string.IsNullOrWhiteSpace(text) ? "[]" : text);
        var items = doc.RootElement.ValueKind == JsonValueKind.Array ? doc.RootElement.EnumerateArray() : new[] { doc.RootElement }.AsEnumerable().Select(e => e);
        foreach (var e in items)
        {
            var name = e.TryGetProperty("Name", out var n) ? n.GetString() : null;
            var id = e.TryGetProperty("AppID", out var i) ? i.GetString() : null;
            if (!string.IsNullOrWhiteSpace(name) && !string.IsNullOrWhiteSpace(id))
                list.Add((name, id));
        }
        lock (CacheLock)
        {
            _cache = list;
            _cacheAt = DateTime.UtcNow;
        }
        return list;
    }

    public static JsonObject Start(JsonObject a)
    {
        var path = a["path"]?.GetValue<string>() ?? "";
        var args = a["args"]?.GetValue<string>() ?? "";
        if (path.Length == 0)
            throw new InvalidOperationException("Не указана программа");
        var dir = "";
        try
        {
            dir = Path.GetDirectoryName(path) ?? "";
        }
        catch
        {
        }
        var psi = new ProcessStartInfo(path, args)
        {
            UseShellExecute = true,
            WorkingDirectory = Directory.Exists(dir) ? dir : Environment.GetFolderPath(Environment.SpecialFolder.UserProfile),
        };
        using var p = Process.Start(psi);
        return new JsonObject { ["pid"] = p?.Id ?? 0 };
    }
}
