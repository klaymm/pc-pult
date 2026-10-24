using System.Text.Json.Nodes;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Messages
{
    public static async Task Handle(Ctx c, string[] a)
    {
        var act = a.Length > 1 ? a[1] : "";
        switch (act)
        {
            case "":
                if (!await c.Require("media"))
                    return;
                await c.Show("💬 <b>Сообщения и буфер обмена</b>", Kb(
                    R(B("🪟 Окно на экран", "w:popup")),
                    R(B("🔊 Сказать вслух", "w:say")),
                    If(c.Has("clipboard"), B("📋 Буфер обмена: показать", "w:cget"), B("📝 Буфер обмена: задать", "w:cset")),
                    BackMenu));
                await c.Toast();
                break;
            case "popup":
            case "say":
            case "cset":
                if (!await c.Require(act == "cset" ? "clipboard" : "media"))
                    return;
                c.App.Pending.Set(c.User.Id, act, c.MsgId.ToString());
                await c.Show(act switch
                {
                    "popup" => "🪟 <b>Окно на экран</b>\nПришлите текст, он появится на ПК поверх всех окон.",
                    "say" => "🔊 <b>Сказать вслух</b>\nПришлите текст, ПК его произнесёт.",
                    _ => "📝 <b>Буфер обмена</b>\nПришлите текст, он окажется в буфере обмена ПК.",
                }, Kb(R(B("Отмена", "w"))));
                await c.Toast();
                break;
            case "cget":
                if (!await c.Require("clipboard"))
                    return;
                await ClipboardGet(c);
                break;
        }
    }

    public static async Task Input(Ctx c, string kind, string text)
    {
        var back = Kb(R(B("💬 Ещё", "w"), B("⬅️ Меню", "menu")));
        switch (kind)
        {
            case "popup":
                if (!c.Has("media"))
                    return;
                await c.App.Agent.Call("popup", new JsonObject { ["title"] = $"Сообщение от {c.User.Name}", ["text"] = text });
                await c.Audit("Сообщение на экран: " + Fmt.Cut(text, 80));
                await c.Show("✅ Окно показано на экране ПК.", back);
                break;
            case "say":
                if (!c.Has("media"))
                    return;
                var res = await c.App.Agent.Call("speak", new JsonObject { ["text"] = text }, 60);
                await c.Audit("Озвучка: " + Fmt.Cut(text, 80));
                var warn = res["warn"]?.GetValue<bool>() == true
                    ? "\n\n⚠️ В Windows нет русского голоса, текст прозвучит с английским акцентом или неразборчиво. Русский голос ставится в Параметры → Время и язык → Речь."
                    : "";
                await c.Show("✅ Сказано." + warn, back);
                break;
            case "cset":
                if (!c.Has("clipboard"))
                    return;
                await c.App.Agent.Call("clipboard_set", new JsonObject { ["text"] = text });
                await c.Audit("Буфер обмена: записан текст");
                await c.Show("✅ Текст в буфере обмена ПК.", back);
                break;
        }
    }

    private static async Task ClipboardGet(Ctx c)
    {
        var res = await c.App.Agent.Call("clipboard_get");
        var kind = res["kind"]!.GetValue<string>();
        await c.Audit("Буфер обмена: прочитан");
        await c.Toast();
        var text = kind switch
        {
            "text" => "📋 <b>Буфер обмена</b>\n\n<pre>" + Fmt.H(Fmt.Cut(res["text"]!.GetValue<string>(), 3000)) + "</pre>",
            "image" => "📋 В буфере обмена картинка. Её можно получить скриншотом экрана.",
            "files" => "📋 В буфере обмена файлы:\n<pre>" + Fmt.H(Fmt.Cut(res["text"]!.GetValue<string>(), 3000)) + "</pre>",
            _ => "📋 Буфер обмена пуст.",
        };
        await c.Show(text, Kb(R(B("🔄 Обновить", "w:cget"), B("⬅️ Назад", "w"))));
    }
}
