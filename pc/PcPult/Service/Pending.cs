using System.Collections.Concurrent;

namespace PcPult.Service;

public sealed class Pending
{
    private static readonly TimeSpan Ttl = TimeSpan.FromMinutes(5);
    private readonly ConcurrentDictionary<long, (string Kind, string Arg, DateTime Until)> _items = new();

    public void Set(long userId, string kind, string arg = "") =>
        _items[userId] = (kind, arg, DateTime.UtcNow + Ttl);

    public (string Kind, string Arg)? Take(long userId)
    {
        if (!_items.TryRemove(userId, out var p) || p.Until < DateTime.UtcNow)
            return null;
        return (p.Kind, p.Arg);
    }

    public void Clear(long userId) => _items.TryRemove(userId, out _);

    public bool Has(long userId) => _items.TryGetValue(userId, out var p) && p.Until >= DateTime.UtcNow;
}
