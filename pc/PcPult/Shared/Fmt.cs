using System.Globalization;
using System.Net;

namespace PcPult.Shared;

public static class Fmt
{
    private static readonly CultureInfo Ru = CultureInfo.GetCultureInfo("ru-RU");

    public static string H(string? s) => WebUtility.HtmlEncode(s ?? "");

    public static string Plural(long n, string one, string few, string many)
    {
        n = Math.Abs(n) % 100;
        if (n is >= 11 and <= 14)
            return many;
        return (n % 10) switch
        {
            1 => one,
            2 or 3 or 4 => few,
            _ => many,
        };
    }

    public static string Dur(TimeSpan d)
    {
        if (d < TimeSpan.Zero)
            d = TimeSpan.Zero;
        var s = (long)d.TotalSeconds;
        if (s < 60)
            return $"{s} с";
        if (s < 3600)
            return $"{s / 60} мин";
        if (s < 86400)
            return s % 3600 / 60 > 0 ? $"{s / 3600} ч {s % 3600 / 60} мин" : $"{s / 3600} ч";
        var days = s / 86400;
        var h = s % 86400 / 3600;
        var dd = $"{days} {Plural(days, "день", "дня", "дней")}";
        return h > 0 ? $"{dd} {h} ч" : dd;
    }

    public static string Size(double bytes)
    {
        string[] units = ["Б", "КБ", "МБ", "ГБ", "ТБ"];
        var i = 0;
        while (bytes >= 1024 && i < units.Length - 1)
        {
            bytes /= 1024;
            i++;
        }
        return i == 0 ? $"{bytes:0} {units[i]}" : bytes.ToString(bytes < 10 ? "0.0" : "0", Ru) + " " + units[i];
    }

    public static string Gb(double bytes) => (bytes / (1024.0 * 1024 * 1024)).ToString("0.0", Ru);

    public static string Num(double v, string format = "0.0") => v.ToString(format, Ru);

    public static string Cut(string s, int max) => s.Length <= max ? s : s[..(max - 1)] + "…";

    public static string Time(DateTime t) => t.ToString("HH:mm", Ru);

    public static string DateTimeShort(DateTime t) => t.ToString("dd.MM.yyyy HH:mm", Ru);
}
