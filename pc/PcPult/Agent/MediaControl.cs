using System.Text.Json.Nodes;
using Windows.Media.Control;

namespace PcPult.Agent;

public static class MediaControl
{
    public static async Task<JsonObject> Run(JsonObject a)
    {
        var op = a["op"]?.GetValue<string>() ?? "now";
        var manager = await GlobalSystemMediaTransportControlsSessionManager.RequestAsync().AsTask();
        var session = manager.GetCurrentSession();
        if (session == null)
            return new JsonObject { ["playing"] = false, ["none"] = true };

        switch (op)
        {
            case "toggle":
                await session.TryTogglePlayPauseAsync().AsTask();
                break;
            case "next":
                await session.TrySkipNextAsync().AsTask();
                break;
            case "prev":
                await session.TrySkipPreviousAsync().AsTask();
                break;
            case "stop":
                await session.TryStopAsync().AsTask();
                break;
        }
        if (op != "now")
            await Task.Delay(400);

        var props = await session.TryGetMediaPropertiesAsync().AsTask();
        var info = session.GetPlaybackInfo();
        return new JsonObject
        {
            ["none"] = false,
            ["playing"] = info.PlaybackStatus == GlobalSystemMediaTransportControlsSessionPlaybackStatus.Playing,
            ["title"] = props.Title,
            ["artist"] = props.Artist,
            ["app"] = session.SourceAppUserModelId,
        };
    }
}
