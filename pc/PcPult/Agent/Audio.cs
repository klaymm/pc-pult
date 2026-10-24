using System.Runtime.InteropServices;
using System.Text.Json.Nodes;
using NAudio.CoreAudioApi;

namespace PcPult.Agent;

public static class Audio
{
    public static JsonObject Volume(JsonObject a)
    {
        using var enumerator = new MMDeviceEnumerator();
        MMDevice device;
        try
        {
            device = enumerator.GetDefaultAudioEndpoint(DataFlow.Render, Role.Multimedia);
        }
        catch (COMException)
        {
            return new JsonObject { ["none"] = true };
        }
        using (device)
        {
            var vol = device.AudioEndpointVolume;
            var op = a["op"]?.GetValue<string>() ?? "get";
            switch (op)
            {
                case "set":
                    vol.MasterVolumeLevelScalar = Math.Clamp((a["value"]?.GetValue<int>() ?? 0) / 100f, 0f, 1f);
                    vol.Mute = false;
                    break;
                case "delta":
                    vol.MasterVolumeLevelScalar = Math.Clamp(vol.MasterVolumeLevelScalar + (a["value"]?.GetValue<int>() ?? 0) / 100f, 0f, 1f);
                    vol.Mute = false;
                    break;
                case "mute":
                    vol.Mute = !vol.Mute;
                    break;
            }
            return new JsonObject
            {
                ["none"] = false,
                ["volume"] = (int)Math.Round(vol.MasterVolumeLevelScalar * 100),
                ["muted"] = vol.Mute,
                ["device"] = device.FriendlyName,
            };
        }
    }
}
