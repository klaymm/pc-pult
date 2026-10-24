using System.Text.Json;
using System.Text.Json.Serialization;

namespace PcPult.Shared;

public sealed class AppEntry
{
    public string Name { get; set; } = "";
    public string Path { get; set; } = "";
    public string Args { get; set; } = "";
    public string Process { get; set; } = "";
}

public sealed class Config
{
    public string PcName { get; set; } = "ПК";
    public string RouterUrl { get; set; } = "http://192.168.1.1:8765";
    public string Key { get; set; } = "";
    public string AnyDeskPath { get; set; } = @"C:\Program Files (x86)\AnyDesk\AnyDesk.exe";
    public string UploadDir { get; set; } = "";
    public int CpuTempAlert { get; set; } = 90;
    public int GpuTempAlert { get; set; } = 85;
    public int DiskFreeAlertGb { get; set; } = 10;
    public List<AppEntry> Apps { get; set; } = [];
    public bool AppsSeeded { get; set; }

    public static string Dir { get; } = System.IO.Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "PcPult");
    public static string FilePath => System.IO.Path.Combine(Dir, "config.json");

    public static readonly JsonSerializerOptions Json = new()
    {
        WriteIndented = true,
        PropertyNamingPolicy = JsonNamingPolicy.SnakeCaseLower,
        DefaultIgnoreCondition = JsonIgnoreCondition.Never,
        Encoder = System.Text.Encodings.Web.JavaScriptEncoder.UnsafeRelaxedJsonEscaping,
    };

    private static readonly Lock SaveLock = new();

    public static Config Load()
    {
        if (!File.Exists(FilePath))
            return new Config();
        return JsonSerializer.Deserialize<Config>(File.ReadAllText(FilePath), Json) ?? new Config();
    }

    public void Save()
    {
        lock (SaveLock)
        {
            var tmp = FilePath + ".tmp";
            File.WriteAllText(tmp, JsonSerializer.Serialize(this, Json));
            File.Move(tmp, FilePath, true);
        }
    }
}
