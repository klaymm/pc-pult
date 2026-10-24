using System.Text.Json.Nodes;
using System.Windows.Forms;

namespace PcPult.Agent;

public static class Clip
{
    public static JsonObject Get()
    {
        if (Clipboard.ContainsText())
            return new JsonObject { ["kind"] = "text", ["text"] = Clipboard.GetText() };
        if (Clipboard.ContainsImage())
            return new JsonObject { ["kind"] = "image" };
        if (Clipboard.ContainsFileDropList())
        {
            var files = Clipboard.GetFileDropList().Cast<string>().ToArray();
            return new JsonObject { ["kind"] = "files", ["text"] = string.Join("\n", files) };
        }
        return new JsonObject { ["kind"] = "empty" };
    }

    public static JsonObject Set(JsonObject a)
    {
        var text = a["text"]?.GetValue<string>() ?? "";
        if (text.Length == 0)
            Clipboard.Clear();
        else
            Clipboard.SetText(text);
        return [];
    }
}
