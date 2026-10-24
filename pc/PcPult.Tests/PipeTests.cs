using System.IO.Pipes;
using System.Text.Json.Nodes;
using PcPult.Shared;

namespace PcPult.Tests;

public class PipeTests
{
    private static async Task<(PipeChannel Server, PipeChannel Client, CancellationTokenSource Cts)> Connect()
    {
        var name = "PcPultTest." + Guid.NewGuid().ToString("N");
        var server = new NamedPipeServerStream(name, PipeDirection.InOut, 1, PipeTransmissionMode.Byte, PipeOptions.Asynchronous);
        var client = new NamedPipeClientStream(".", name, PipeDirection.InOut, PipeOptions.Asynchronous);
        await Task.WhenAll(server.WaitForConnectionAsync(), client.ConnectAsync(3000));
        var cts = new CancellationTokenSource();
        var s = new PipeChannel(server);
        var c = new PipeChannel(client);
        _ = s.RunAsync(cts.Token);
        _ = c.RunAsync(cts.Token);
        return (s, c, cts);
    }

    [Fact]
    public async Task RequestGetsResponse()
    {
        var (server, client, cts) = await Connect();
        client.OnRequest = (op, args) => Task.FromResult(new JsonObject { ["echo"] = op + ":" + args["x"]!.GetValue<int>() });
        var res = await server.Call("ping", new JsonObject { ["x"] = 7 }, TimeSpan.FromSeconds(3));
        Assert.Equal("ping:7", res["echo"]!.GetValue<string>());
        cts.Cancel();
    }

    [Fact]
    public async Task HandlerErrorBecomesAgentException()
    {
        var (server, client, cts) = await Connect();
        client.OnRequest = (_, _) => throw new InvalidOperationException("нет звука");
        var ex = await Assert.ThrowsAsync<AgentException>(() => server.Call("volume", null, TimeSpan.FromSeconds(3)));
        Assert.Equal("нет звука", ex.Message);
        cts.Cancel();
    }

    [Fact]
    public async Task EventsArriveWithoutReply()
    {
        var (server, client, cts) = await Connect();
        var got = new TaskCompletionSource<string>();
        server.OnEvent = (op, args) => got.TrySetResult(op + ":" + args["user"]!.GetValue<string>());
        await client.Event("hello", new JsonObject { ["user"] = "klaymm" });
        Assert.Equal("hello:klaymm", await got.Task.WaitAsync(TimeSpan.FromSeconds(3)));
        cts.Cancel();
    }

    [Fact]
    public async Task SlowHandlerTimesOut()
    {
        var (server, client, cts) = await Connect();
        client.OnRequest = async (_, _) =>
        {
            await Task.Delay(2000);
            return [];
        };
        await Assert.ThrowsAsync<AgentException>(() => server.Call("slow", null, TimeSpan.FromMilliseconds(200)));
        cts.Cancel();
    }
}
