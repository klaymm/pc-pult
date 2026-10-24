using System.Text;

namespace PcPult.Shared;

public static class Log
{
    private const long MaxSize = 2 << 20;
    private static readonly Lock Sync = new();
    private static string? _path;

    public static void Init(string name)
    {
        var dir = Path.Combine(Config.Dir, "logs");
        try
        {
            Directory.CreateDirectory(dir);
            _path = Path.Combine(dir, name + ".log");
            File.AppendAllText(_path, "");
        }
        catch
        {
            dir = Path.Combine(Path.GetTempPath(), "PcPult");
            Directory.CreateDirectory(dir);
            _path = Path.Combine(dir, name + ".log");
        }
    }

    public static void Info(string msg) => Write("INFO", msg);
    public static void Warn(string msg) => Write("WARN", msg);
    public static void Error(string msg, Exception? ex = null) => Write("ERROR", ex == null ? msg : $"{msg}: {ex}");

    private static void Write(string level, string msg)
    {
        if (_path == null)
            return;
        var line = $"{DateTime.Now:yyyy-MM-dd HH:mm:ss} {level} {msg}{Environment.NewLine}";
        lock (Sync)
        {
            try
            {
                var fi = new FileInfo(_path);
                if (fi.Exists && fi.Length > MaxSize)
                    File.Move(_path, _path + ".1", true);
                File.AppendAllText(_path, line, Encoding.UTF8);
            }
            catch
            {
            }
        }
    }
}
