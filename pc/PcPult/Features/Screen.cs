using System.Text.Json.Nodes;
using Telegram.Bot;
using Telegram.Bot.Types.Enums;
using Telegram.Bot.Types.ReplyMarkups;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Screen
{
    public static async Task Handle(Ctx c, string[] a)
    {
        if (!await c.Require("screen"))
            return;
        var act = a.Length > 1 ? a[1] : "";
        switch (act)
        {
            case "":
                await Shot(c, -1, false);
                break;
            case "pick":
                await Pick(c);
                break;
            case "o":
                await Shot(c, a.Length > 2 && int.TryParse(a[2], out var oi) ? oi : -1, true);
                break;
            default:
                await Shot(c, int.TryParse(act, out var idx) ? idx : -1, false);
                break;
        }
    }

    private static async Task Pick(Ctx c)
    {
        var res = await c.App.Agent.Call("screens");
        var buttons = new List<InlineKeyboardButton[]> { R(B("🖥 Все экраны", "ss")) };
        foreach (var s in res["screens"]!.AsArray())
        {
            var i = s!["index"]!.GetValue<int>();
            var title = $"Экран {i + 1} · {s["width"]}×{s["height"]}" + (s["primary"]!.GetValue<bool>() ? " (основной)" : "");
            buttons.Add(R(B(title, $"ss:{i}")));
        }
        buttons.Add(BackMenu);
        await c.Show("📸 <b>Какой экран снять?</b>", new InlineKeyboardMarkup(buttons));
        await c.Toast();
    }

    private static async Task Shot(Ctx c, int monitor, bool original)
    {
        await c.Toast("Снимаю...");
        await c.Bot.SendChatAction(c.ChatId, ChatAction.UploadPhoto, cancellationToken: c.Ct);
        var res = await c.App.Agent.Call("screenshot", new JsonObject
        {
            ["monitor"] = monitor,
            ["original"] = original,
        }, 30, c.Ct);
        var bytes = Convert.FromBase64String(res["data"]!.GetValue<string>());
        var ext = res["ext"]!.GetValue<string>();
        var caption = $"📸 {Fmt.H(c.App.Cfg.PcName)} · {Fmt.DateTimeShort(DateTime.Now)} · {res["width"]}×{res["height"]}";
        var kb = Kb(
            R(B("🔄 Ещё раз", monitor < 0 ? "ss" : $"ss:{monitor}"), B("📄 Оригинал файлом", $"ss:o:{monitor}")),
            R(B("🖥 Выбрать экран", "ss:pick"), B("⬅️ Меню", "menu")));
        await c.Audit(original ? "Скриншот (оригинал)" : "Скриншот");
        await c.ShowPhoto(bytes, $"screen-{DateTime.Now:yyyyMMdd-HHmmss}.{ext}", caption, kb, original);
    }
}
