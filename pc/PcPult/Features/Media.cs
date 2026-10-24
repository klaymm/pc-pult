using System.Text;
using System.Text.Json.Nodes;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Media
{
    public static async Task Handle(Ctx c, string[] a)
    {
        if (!await c.Require("media"))
            return;
        var act = a.Length > 1 ? a[1] : "";
        switch (act)
        {
            case "v":
                await Volume(c, a);
                break;
            case "p":
                await Playback(c, a.Length > 2 ? a[2] : "");
                break;
        }
        await View(c);
    }

    private static async Task Volume(Ctx c, string[] a)
    {
        var op = a.Length > 2 ? a[2] : "";
        var args = new JsonObject { ["op"] = op };
        var v = 0;
        if (a.Length > 3 && int.TryParse(a[3], out v))
            args["value"] = v;
        var res = await c.App.Agent.Call("volume", args);
        if (res["none"]?.GetValue<bool>() == true)
        {
            await c.Toast("Нет устройства вывода звука", true);
            return;
        }
        await c.Audit(op switch
        {
            "mute" => "Звук: вкл/выкл",
            "set" => $"Громкость {v}%",
            _ => $"Громкость {(v > 0 ? "+" : "")}{v}",
        });
    }

    private static async Task Playback(Ctx c, string op)
    {
        if (op is not ("toggle" or "next" or "prev"))
            return;
        await c.App.Agent.Call("media", new JsonObject { ["op"] = op }, 15);
        await c.Audit("Музыка: " + op switch { "toggle" => "пауза/плей", "next" => "следующий трек", _ => "предыдущий трек" });
    }

    private static async Task View(Ctx c)
    {
        var vol = await c.App.Agent.Call("volume", new JsonObject { ["op"] = "get" });
        JsonObject? now = null;
        try
        {
            now = await c.App.Agent.Call("media", new JsonObject { ["op"] = "now" }, 10);
        }
        catch (AgentException ex)
        {
            Log.Warn("media: " + ex.Message);
        }
        var noDevice = vol["none"]?.GetValue<bool>() == true;
        var level = noDevice ? 0 : vol["volume"]!.GetValue<int>();
        var muted = !noDevice && vol["muted"]!.GetValue<bool>();
        var sb = new StringBuilder("🔊 <b>Звук и музыка</b>\n\n");
        if (noDevice)
        {
            sb.AppendLine("🎧 Устройство вывода звука не найдено (наушники или колонки отключены). Громкость заработает, когда оно появится.");
        }
        else
        {
            sb.Append(muted ? "🔇 Звук выключен" : $"🔊 Громкость {level}%").Append(Bar(muted ? 0 : level)).AppendLine();
            sb.AppendLine($"<i>{Fmt.H(vol["device"]?.GetValue<string>())}</i>");
        }
        sb.AppendLine();
        if (now == null || now["none"]?.GetValue<bool>() == true)
        {
            sb.Append("🎵 Сейчас ничего не играет");
        }
        else
        {
            var title = now["title"]?.GetValue<string>();
            var artist = now["artist"]?.GetValue<string>();
            var state = now["playing"]!.GetValue<bool>() ? "▶️" : "⏸";
            sb.Append($"{state} <b>{Fmt.H(string.IsNullOrWhiteSpace(title) ? "Без названия" : title)}</b>");
            if (!string.IsNullOrWhiteSpace(artist))
                sb.Append("\n" + Fmt.H(artist));
        }
        await c.Show(sb.ToString(), Kb(
            R(B("🔉 −10", "m:v:delta:-10"), B(muted ? "🔊 Включить" : "🔇 Выключить", "m:v:mute"), B("🔊 +10", "m:v:delta:10")),
            R(B("0%", "m:v:set:0"), B("25%", "m:v:set:25"), B("50%", "m:v:set:50"), B("75%", "m:v:set:75"), B("100%", "m:v:set:100")),
            R(B("⏮", "m:p:prev"), B("⏯", "m:p:toggle"), B("⏭", "m:p:next")),
            R(B("🔄 Обновить", "m"), B("⬅️ Меню", "menu"))));
        await c.Toast();
    }

    private static string Bar(int percent)
    {
        var filled = Math.Clamp(percent / 10, 0, 10);
        return "\n" + new string('█', filled) + new string('░', 10 - filled);
    }
}
