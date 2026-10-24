namespace PcPult.Service;

public static class Backoff
{
    public static readonly TimeSpan First = TimeSpan.FromMilliseconds(500);
    public static readonly TimeSpan Max = TimeSpan.FromSeconds(5);

    public static TimeSpan Next(TimeSpan current) =>
        TimeSpan.FromMilliseconds(Math.Min(current.TotalMilliseconds * 2, Max.TotalMilliseconds));
}
