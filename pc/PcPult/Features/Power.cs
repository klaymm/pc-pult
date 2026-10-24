using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Power
{
    private static DateTime? _timerEnd;
    private static string _timerKind = "";

    public static async Task Handle(Ctx c, string[] a)
    {
        if (!await c.Require("power"))
            return;
        var act = a.Length > 1 ? a[1] : "";
        switch (act)
        {
            case "":
                await Menu(c);
                break;
            case "off":
                await Confirm(c, "⏻ <b>Выключить ПК?</b>", "p:offy", "p:offf", "Выключить");
                break;
            case "rst":
                await Confirm(c, "🔄 <b>Перезагрузить ПК?</b>", "p:rsty", "p:rstf", "Перезагрузить");
                break;
            case "slp":
                await Confirm(c, "😴 <b>Отправить ПК в сон?</b>\nОн перестанет отвечать, пока вы его не включите.", "p:slpy", null, "В сон");
                break;
            case "hib":
                await Confirm(c, "🧊 <b>Гибернация?</b>\nСостояние сохранится на диск, ПК выключится.", "p:hiby", null, "Гибернация");
                break;
            case "out":
                await Confirm(c, "🚪 <b>Выйти из системы?</b>\nНесохранённые данные в программах могут пропасть.", "p:outy", null, "Выйти");
                break;
            case "offy": await Run(c, "shutdown", false); break;
            case "offf": await Run(c, "shutdown", true); break;
            case "rsty": await Run(c, "restart", false); break;
            case "rstf": await Run(c, "restart", true); break;
            case "slpy": await Run(c, "sleep", false); break;
            case "hiby": await Run(c, "hibernate", false); break;
            case "outy": await LogOff(c); break;
            case "lock": await Lock(c); break;
            case "mon": await MonitorOff(c); break;
            case "tm": await TimerMenu(c, a); break;
            case "tmc": await TimerCancel(c); break;
        }
    }

    private static async Task Menu(Ctx c)
    {
        var text = "⚡ <b>Питание</b>";
        var timerOn = _timerEnd is { } end && end > DateTime.Now;
        if (timerOn)
            text += $"\n\n⏲ Таймер: {TimerName(_timerKind)} в {Fmt.Time(_timerEnd!.Value)} (через {Fmt.Dur(_timerEnd.Value - DateTime.Now)})";
        var hib = Native.IsPwrHibernateAllowed();
        await c.Show(text, Kb(
            R(B("⏻ Выключить", "p:off"), B("🔄 Перезагрузить", "p:rst")),
            R(B("😴 Сон", "p:slp"), hib ? B("🧊 Гибернация", "p:hib") : B("🧊 Гибернация (выкл)", "x")),
            R(B("🔒 Заблокировать", "p:lock"), B("🖥 Выключить монитор", "p:mon")),
            R(B("🚪 Выйти из системы", "p:out"), B("⏲ Таймер", "p:tm")),
            If(timerOn, B("❌ Отменить таймер", "p:tmc")),
            BackMenu));
        await c.Toast();
    }

    private static async Task Confirm(Ctx c, string text, string yes, string? force, string yesTitle)
    {
        await c.Show(text, Kb(
            R(B("✅ " + yesTitle, yes)),
            If(force != null, B("⚠️ Принудительно (закрыть программы)", force!)),
            R(B("Отмена", "p"))));
        await c.Toast();
    }

    private static string TimerName(string kind) => kind == "restart" ? "перезагрузка" : "выключение";

    private static async Task Run(Ctx c, string kind, bool force)
    {
        var (title, audit) = kind switch
        {
            "shutdown" => ("🔴 <b>Выключаю ПК...</b>", "Выключение ПК"),
            "restart" => ("🔄 <b>Перезагружаю ПК...</b>", "Перезагрузка ПК"),
            "sleep" => ("😴 <b>Отправляю ПК в сон...</b>", "Сон"),
            _ => ("🧊 <b>Гибернация...</b>", "Гибернация"),
        };
        await c.Toast();
        await c.Audit(audit + (force ? " (принудительно)" : ""));
        await c.Show(title);
        var error = await Execute(c.App, kind, force, c.User.Id, "из Telegram");
        if (error != null)
            await c.Show($"⚠️ <b>Не получилось</b>\n{Fmt.H(error)}", Kb(BackMenu));
    }

    private static async Task<string?> Execute(BotApp app, string kind, bool force, long by, string source)
    {
        var reason = kind switch { "shutdown" => "shutdown", "restart" => "restart", "sleep" => "sleep", _ => "hibernate" };
        _timerEnd = null;
        if (kind is "shutdown" or "restart")
            await Service.BotApp.RunProcess("shutdown.exe", "/a");
        await app.Bye(reason, by);
        await Task.Delay(800);
        switch (kind)
        {
            case "shutdown":
            case "restart":
                var flag = kind == "shutdown" ? "/s" : "/r";
                var verb = kind == "shutdown" ? "Выключение" : "Перезагрузка";
                var (code, output) = await Service.BotApp.RunProcess("shutdown.exe", $"{flag} /t 3 {(force ? "/f " : "")}/c \"{verb} по команде {source}\"");
                if (code != 0)
                {
                    app.Resume();
                    return Fmt.Cut(output, 150);
                }
                break;
            case "sleep":
                Native.EnablePrivilege("SeShutdownPrivilege");
                Native.SetSuspendState(false, false, false);
                break;
            default:
                Native.EnablePrivilege("SeShutdownPrivilege");
                Native.SetSuspendState(true, false, false);
                break;
        }
        return null;
    }

    private static async Task LogOff(Ctx c)
    {
        await c.Toast();
        var session = Native.WTSGetActiveConsoleSessionId();
        if (session == 0xFFFFFFFF || Native.SessionUser(session) == null)
        {
            await c.Show("В Windows сейчас никто не вошёл.", Kb(R(B("⬅️ Назад", "p"))));
            return;
        }
        await c.Audit("Выход из системы");
        Native.WTSLogoffSession(Native.WtsCurrentServer, session, false);
        await c.Show("🚪 Выход из системы выполнен.", Kb(R(B("⬅️ Назад", "p"))));
    }

    private static async Task Lock(Ctx c)
    {
        try
        {
            await c.App.Agent.Call("lock");
            await c.Audit("Блокировка ПК");
            await c.Toast("Заблокировано");
            await Menu(c);
        }
        catch (AgentException ex)
        {
            await c.Toast(ex.Message, true);
        }
    }

    private static async Task MonitorOff(Ctx c)
    {
        try
        {
            await c.App.Agent.Call("monitor_off");
            await c.Audit("Монитор выключен");
            await c.Toast("Монитор выключен");
        }
        catch (AgentException ex)
        {
            await c.Toast(ex.Message, true);
        }
    }

    private static async Task TimerMenu(Ctx c, string[] a)
    {
        if (a.Length > 3 && int.TryParse(a[3], out var minutes) && minutes is > 0 and <= 7 * 24 * 60)
        {
            var kind = a[2] == "r" ? "restart" : "shutdown";
            var flag = kind == "restart" ? "/r" : "/s";
            var (code, output) = await Service.BotApp.RunProcess("shutdown.exe", $"/a");
            _ = code;
            _ = output;
            var (code2, output2) = await Service.BotApp.RunProcess("shutdown.exe", $"{flag} /t {minutes * 60} /c \"Запланировано из Telegram\"");
            if (code2 != 0)
            {
                await c.Toast("Не получилось поставить таймер: " + Fmt.Cut(output2, 150), true);
                return;
            }
            _timerEnd = DateTime.Now.AddMinutes(minutes);
            _timerKind = kind;
            await c.Audit($"Таймер: {TimerName(kind)} через {minutes} мин");
            await c.Toast($"Таймер на {Fmt.Dur(TimeSpan.FromMinutes(minutes))} поставлен");
            await Menu(c);
            return;
        }
        var mode = a.Length > 2 ? a[2] : "";
        if (mode != "s" && mode != "r")
        {
            await c.Show("⏲ <b>Таймер</b>\nЧто сделать по таймеру?", Kb(
                R(B("⏻ Выключить", "p:tm:s"), B("🔄 Перезагрузить", "p:tm:r")),
                R(B("⬅️ Назад", "p"))));
            await c.Toast();
            return;
        }
        var verb = mode == "s" ? "выключить" : "перезагрузить";
        await c.Show($"⏲ <b>Через сколько {verb} ПК?</b>", Kb(
            R(B("15 мин", $"p:tm:{mode}:15"), B("30 мин", $"p:tm:{mode}:30"), B("1 час", $"p:tm:{mode}:60")),
            R(B("2 часа", $"p:tm:{mode}:120"), B("3 часа", $"p:tm:{mode}:180"), B("5 часов", $"p:tm:{mode}:300")),
            R(B("⬅️ Назад", "p:tm"))));
        await c.Toast();
    }

    private static async Task TimerCancel(Ctx c)
    {
        await Service.BotApp.RunProcess("shutdown.exe", "/a");
        _timerEnd = null;
        await c.Audit("Таймер отменён");
        await c.Toast("Таймер отменён");
        await Menu(c);
    }
}
