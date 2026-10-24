using System.Collections.Concurrent;

namespace PcPult.Service;

public sealed class PathIds
{
    private const int Capacity = 3000;
    private readonly ConcurrentDictionary<string, string> _byId = new();
    private readonly ConcurrentDictionary<string, string> _byPath = new(StringComparer.OrdinalIgnoreCase);
    private readonly ConcurrentQueue<string> _order = new();
    private long _next;

    public string Id(string path)
    {
        if (_byPath.TryGetValue(path, out var existing))
            return existing;
        var id = Base36(Interlocked.Increment(ref _next));
        if (!_byPath.TryAdd(path, id))
            return _byPath[path];
        _byId[id] = path;
        _order.Enqueue(id);
        while (_order.Count > Capacity && _order.TryDequeue(out var old))
        {
            if (_byId.TryRemove(old, out var oldPath))
                _byPath.TryRemove(oldPath, out _);
        }
        return id;
    }

    public string? Path(string id) => _byId.GetValueOrDefault(id);

    private static string Base36(long n)
    {
        const string digits = "0123456789abcdefghijklmnopqrstuvwxyz";
        if (n == 0)
            return "0";
        var chars = new Stack<char>();
        for (; n > 0; n /= 36)
            chars.Push(digits[(int)(n % 36)]);
        return new string(chars.ToArray());
    }
}
