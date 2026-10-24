using System.Collections.Concurrent;
using System.Text;
using System.Text.Json;
using System.Text.Json.Nodes;

namespace PcPult.Shared;

public sealed class PipeMsg
{
    public string T { get; set; } = "";
    public long Id { get; set; }
    public string Op { get; set; } = "";
    public JsonObject? A { get; set; }
    public string? Err { get; set; }
}

public sealed class PipeChannel : IDisposable
{
    public const string Name = "PcPult.Agent";

    private static readonly JsonSerializerOptions Json = new() { PropertyNamingPolicy = JsonNamingPolicy.CamelCase };
    private readonly Stream _stream;
    private readonly StreamReader _reader;
    private readonly SemaphoreSlim _write = new(1, 1);
    private readonly ConcurrentDictionary<long, TaskCompletionSource<JsonObject>> _pending = new();
    private long _nextId;

    public Func<string, JsonObject, Task<JsonObject>>? OnRequest { get; set; }
    public Action<string, JsonObject>? OnEvent { get; set; }

    public PipeChannel(Stream stream)
    {
        _stream = stream;
        _reader = new StreamReader(stream, new UTF8Encoding(false), false, 1 << 16, true);
    }

    public async Task RunAsync(CancellationToken ct)
    {
        try
        {
            while (!ct.IsCancellationRequested)
            {
                var line = await _reader.ReadLineAsync(ct);
                if (line == null)
                    break;
                var msg = JsonSerializer.Deserialize<PipeMsg>(line, Json);
                if (msg == null)
                    continue;
                switch (msg.T)
                {
                    case "res":
                        if (_pending.TryRemove(msg.Id, out var tcs))
                        {
                            if (msg.Err != null)
                                tcs.TrySetException(new AgentException(msg.Err));
                            else
                                tcs.TrySetResult(msg.A ?? []);
                        }
                        break;
                    case "req":
                        _ = HandleRequest(msg);
                        break;
                    case "evt":
                        try
                        {
                            OnEvent?.Invoke(msg.Op, msg.A ?? []);
                        }
                        catch (Exception ex)
                        {
                            Log.Error("pipe event " + msg.Op, ex);
                        }
                        break;
                }
            }
        }
        finally
        {
            foreach (var p in _pending.Values)
                p.TrySetException(new AgentException("Помощник отключился"));
            _pending.Clear();
        }
    }

    private async Task HandleRequest(PipeMsg req)
    {
        var res = new PipeMsg { T = "res", Id = req.Id, Op = req.Op };
        try
        {
            res.A = OnRequest == null ? [] : await OnRequest(req.Op, req.A ?? []);
        }
        catch (Exception ex)
        {
            res.Err = ex.Message;
        }
        await Send(res);
    }

    public async Task<JsonObject> Call(string op, JsonObject? args, TimeSpan timeout, CancellationToken ct = default)
    {
        var id = Interlocked.Increment(ref _nextId);
        var tcs = new TaskCompletionSource<JsonObject>(TaskCreationOptions.RunContinuationsAsynchronously);
        _pending[id] = tcs;
        await Send(new PipeMsg { T = "req", Id = id, Op = op, A = args ?? [] });
        using var cts = CancellationTokenSource.CreateLinkedTokenSource(ct);
        cts.CancelAfter(timeout);
        await using var reg = cts.Token.Register(() =>
        {
            if (_pending.TryRemove(id, out var p))
                p.TrySetException(new AgentException("Помощник не ответил вовремя"));
        });
        return await tcs.Task;
    }

    public Task Event(string op, JsonObject args) => Send(new PipeMsg { T = "evt", Op = op, A = args });

    private async Task Send(PipeMsg msg)
    {
        var bytes = Encoding.UTF8.GetBytes(JsonSerializer.Serialize(msg, Json) + "\n");
        await _write.WaitAsync();
        try
        {
            await _stream.WriteAsync(bytes);
            await _stream.FlushAsync();
        }
        finally
        {
            _write.Release();
        }
    }

    public void Dispose()
    {
        _reader.Dispose();
        _stream.Dispose();
    }
}

public sealed class AgentException(string message) : Exception(message);
