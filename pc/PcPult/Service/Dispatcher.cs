using System.Text.Json;
using Telegram.Bot;
using Telegram.Bot.Types;
using Telegram.Bot.Types.ReplyMarkups;
using PcPult.Features;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Service;

public sealed class Dispatcher
{
    private readonly BotApp _app;

    public Dispatcher(BotApp app) => _app = app;

    public async Task Handle(PollItem item, CancellationToken ct)
    {
        try
        {
            var update = JsonSerializer.Deserialize<Update>(item.Update.GetRawText(), JsonBotAPI.Options);
            if (update == null)
                return;
            if (update.CallbackQuery is { } cq && cq.Message != null)
            {
                var cm = cq.Message;
                var c = new Ctx
                {
                    App = _app, User = item.User, ChatId = cm.Chat.Id, MsgId = cm.Id, PanelId = item.Panel,
                    MsgIsMedia = cm.Photo != null || cm.Document != null || cm.Video != null,
                    CallbackId = cq.Id, Message = cm, Ct = ct,
                };
                await Callback(c, cq.Data ?? "");
            }
            else if (update.Message is { } m)
            {
                var c = new Ctx { App = _app, User = item.User, ChatId = m.Chat.Id, PanelId = item.Panel, Message = m, Ct = ct };
                await Message(c, m);
            }
        }
        catch (Exception ex)
        {
            Log.Error("dispatch", ex);
        }
    }

    private async Task Callback(Ctx c, string data)
    {
        var a = data.Split(':');
        _app.Navigated(c.User.Id);
        _app.Pending.Clear(c.User.Id);
        try
        {
            switch (a[0])
            {
                case "menu": await Menu(c); break;
                case "p": await Power.Handle(c, a); break;
                case "s": await StatusView(c); break;
                case "ss": await Features.Screen.Handle(c, a); break;
                case "m": await Media.Handle(c, a); break;
                case "w": await Messages.Handle(c, a); break;
                case "a": await Apps.Handle(c, a); break;
                case "ad": await AnyDesk.Handle(c, a); break;
                case "f": await Files.Handle(c, a); break;
                default: await c.Toast(); break;
            }
        }
        catch (AgentException ex)
        {
            await c.Toast(ex.Message, true);
        }
        catch (Exception ex)
        {
            Log.Error("callback " + data, ex);
            await c.Toast("Ошибка: " + Fmt.Cut(ex.Message, 150), true);
        }
    }

    private async Task Message(Ctx c, Message m)
    {
        var text = m.Text ?? "";
        try
        {
            if (Files.HasAttachment(m))
            {
                await Files.Upload(c, m);
                return;
            }
            if (!text.StartsWith('/'))
            {
                if (_app.Pending.Take(c.User.Id) is { } p)
                {
                    if (int.TryParse(p.Arg, out var panel))
                        c.MsgId = panel;
                    if (Apps.InputKinds.Contains(p.Kind))
                        await Apps.Input(c, p.Kind, text);
                    else if (p.Kind == "procsearch")
                        await Processes.Input(c, text);
                    else if (AnyDesk.InputKinds.Contains(p.Kind))
                        await AnyDesk.Input(c, p.Kind, text);
                    else
                        await Messages.Input(c, p.Kind, text);
                }
                return;
            }
            _app.Navigated(c.User.Id);
            _app.Pending.Clear(c.User.Id);
            var cmd = text.Split(' ', 2)[0].Split('@')[0].ToLowerInvariant();
            switch (cmd)
            {
                case "/start":
                case "/menu":
                case "/help":
                    await Menu(c);
                    break;
                case "/off": await Power.Handle(c, ["p", "off"]); break;
                case "/status": await StatusView(c); break;
                case "/screen": await Features.Screen.Handle(c, ["ss"]); break;
                case "/sound": await Media.Handle(c, ["m"]); break;
                case "/anydesk": await AnyDesk.Handle(c, ["ad"]); break;
                case "/apps": await Apps.Handle(c, ["a"]); break;
                case "/files": await Files.Handle(c, ["f"]); break;
            }
        }
        catch (AgentException ex)
        {
            await c.Show("⚠️ " + Fmt.H(ex.Message), Kb(BackMenu));
        }
        catch (Exception ex)
        {
            Log.Error("message " + text, ex);
            await c.Show("⚠️ Ошибка: " + Fmt.H(Fmt.Cut(ex.Message, 200)), Kb(BackMenu));
        }
    }

    private async Task Menu(Ctx c)
    {
        var head = $"🟢 <b>{Fmt.H(_app.Cfg.PcName)}</b> · работает {Fmt.Dur(BotApp.Uptime)}";
        var items = new List<Telegram.Bot.Types.ReplyMarkups.InlineKeyboardButton>();
        if (c.Has("status"))
            items.Add(B("📊 Состояние", "s"));
        if (c.Has("power"))
            items.Add(B("⚡ Питание", "p"));
        if (c.Has("screen"))
            items.Add(B("📸 Скриншот", "ss"));
        if (c.Has("media"))
        {
            items.Add(B("🔊 Звук и музыка", "m"));
            items.Add(B("💬 Сообщения", "w"));
        }
        if (c.Has("apps"))
            items.Add(B("🚀 Программы", "a"));
        if (c.Has("files"))
            items.Add(B("📁 Файлы", "f"));
        if (c.Has("anydesk"))
            items.Add(B("🖥 AnyDesk", "ad"));
        items.Add(B("⚙️ Настройки", "r:set"));
        await c.Show(head, new Telegram.Bot.Types.ReplyMarkups.InlineKeyboardMarkup(items.Chunk(2)));
        await c.Toast();
    }

    private async Task StatusView(Ctx c)
    {
        if (!await c.Require("status"))
            return;
        var snap = await _app.Status.Take();
        var session = Native.WTSGetActiveConsoleSessionId();
        var user = session == 0xFFFFFFFF ? null : Native.SessionUser(session);
        string userLine;
        if (user == null)
        {
            userLine = "👤 Никто не вошёл (экран входа)";
        }
        else
        {
            userLine = $"👤 {Fmt.H(user)}";
            try
            {
                var info = await _app.Agent.Call("info", null, 5);
                userLine += info["locked"]!.GetValue<bool>() ? " · 🔒 заблокирован" : " · разблокирован";
                var idle = info["idle_sec"]!.GetValue<int>();
                if (idle >= 60)
                    userLine += $" · не трогали {Fmt.Dur(TimeSpan.FromSeconds(idle))}";
                var window = info["window"]?.GetValue<string>();
                if (!string.IsNullOrWhiteSpace(window) && info["locked"]!.GetValue<bool>() == false)
                    userLine += $"\n🪟 {Fmt.H(Fmt.Cut(window, 70))}";
            }
            catch (AgentException)
            {
            }
        }
        await c.Show(StatusService.Text(snap, _app.Cfg.PcName, userLine), Kb(
            R(B("🔄 Обновить", "s")),
            BackMenu));
        await c.Toast();
    }
}
