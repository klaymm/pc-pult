using System.Text.Json.Nodes;
using Telegram.Bot.Types.ReplyMarkups;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Apps
{
    private static readonly Lock Sync = new();
    private static readonly Dictionary<long, List<(string Name, string Path, string Args)>> Found = [];

    public static readonly HashSet<string> InputKinds = ["appsearch", "appadd"];

    private static readonly Dictionary<string, string[]> Synonyms = new()
    {
        ["калькулятор"] = ["calculator"],
        ["блокнот"] = ["notepad"],
        ["проводник"] = ["file explorer", "explorer"],
        ["диспетчер"] = ["task manager"],
        ["настройки"] = ["settings"],
        ["параметры"] = ["settings"],
        ["панель управления"] = ["control panel"],
        ["командная строка"] = ["command prompt"],
        ["терминал"] = ["terminal", "powershell"],
        ["паинт"] = ["paint"],
        ["рисование"] = ["paint"],
        ["ножницы"] = ["snipping"],
        ["календарь"] = ["calendar"],
        ["почта"] = ["mail", "outlook"],
        ["камера"] = ["camera"],
        ["часы"] = ["clock"],
        ["будильник"] = ["alarms"],
        ["карты"] = ["maps"],
        ["фото"] = ["photos"],
        ["музыка"] = ["media player", "groove", "spotify"],
        ["браузер"] = ["edge", "chrome", "firefox"],
        ["магазин"] = ["store"],
        ["стим"] = ["steam"],
        ["хром"] = ["chrome"],
        ["телеграм"] = ["telegram"],
        ["дискорд"] = ["discord"],
        ["ворд"] = ["word"],
        ["эксель"] = ["excel"],
        ["пауэрпоинт"] = ["powerpoint"],
        ["видео"] = ["video", "media player"],
        ["диск"] = ["disk"],
        ["запись"] = ["recorder"],
    };

    private static List<string> Terms(string query)
    {
        var q = query.Trim().ToLowerInvariant();
        var terms = new List<string> { q };
        foreach (var (key, values) in Synonyms)
        {
            if (q.Contains(key) || key.Contains(q) && q.Length >= 4)
                terms.AddRange(values);
        }
        return terms.Distinct().ToList();
    }

    public static async Task Handle(Ctx c, string[] a)
    {
        if (!await c.Require("apps"))
            return;
        EnsureSeeded(c.App.Cfg);
        var act = a.Length > 1 ? a[1] : "";
        var arg = a.Length > 2 ? a[2] : "";
        switch (act)
        {
            case "":
                await Menu(c);
                break;
            case "r":
                await Run(c, arg);
                break;
            case "s":
                c.App.Pending.Set(c.User.Id, "appsearch", c.MsgId.ToString());
                await c.Show("🔎 <b>Найти программу</b>\nНапишите часть названия, например <code>steam</code> или <code>калькулятор</code>. Поиск идёт по ярлыкам меню \"Пуск\".", Kb(R(B("Отмена", "a"))));
                await c.Toast();
                break;
            case "rl":
                await RunFound(c, arg);
                break;
            case "add":
                c.App.Pending.Set(c.User.Id, "appadd", c.MsgId.ToString());
                await c.Show("➕ <b>Добавить в избранное</b>\nПришлите полный путь к программе, например <code>C:\\Games\\game.exe</code>, или название для поиска в меню \"Пуск\".", Kb(R(B("Отмена", "a"))));
                await c.Toast();
                break;
            case "ad":
                await AddFound(c, arg);
                break;
            case "del":
                await DeleteList(c);
                break;
            case "dy":
                await DeleteApp(c, arg);
                break;
            case "ps":
            case "pss":
            case "k":
            case "ky":
                await Processes.Handle(c, a);
                break;
        }
    }

    public static async Task Input(Ctx c, string kind, string text)
    {
        if (!c.Has("apps"))
            return;
        text = text.Trim();
        if (kind == "appadd" && (text.Contains('\\') || text.Contains('/')) && text.Length > 3)
        {
            var path = text.Trim('"');
            if (!File.Exists(path))
            {
                c.App.Pending.Set(c.User.Id, kind, c.MsgId.ToString());
                await c.Show($"Файл не найден: <code>{Fmt.H(path)}</code>\nПроверьте путь и пришлите ещё раз.", Kb(R(B("Отмена", "a"))));
                return;
            }
            AddApp(c.App.Cfg, Path.GetFileNameWithoutExtension(path), path);
            await c.Audit("Программа добавлена в избранное: " + path);
            await Menu(c);
            return;
        }
        var results = await Search(c.App, text);
        lock (Sync)
            Found[c.User.Id] = results;
        if (results.Count == 0)
        {
            c.App.Pending.Set(c.User.Id, kind, c.MsgId.ToString());
            await c.Show($"По запросу «{Fmt.H(text)}» ничего не нашлось. Попробуйте другое слово.", Kb(R(B("Отмена", "a"))));
            return;
        }
        var verb = kind == "appadd" ? "ad" : "rl";
        var title = kind == "appadd" ? "Что добавить в избранное?" : "Что запустить?";
        var rows = new List<InlineKeyboardButton[]>();
        for (var i = 0; i < results.Count; i++)
            rows.Add(R(B((kind == "appadd" ? "➕ " : "▶️ ") + Fmt.Cut(results[i].Name, 40), $"a:{verb}:{i}")));
        rows.Add(R(B("⬅️ Назад", "a")));
        await c.Show($"🔎 <b>{title}</b>", new InlineKeyboardMarkup(rows));
    }

    private static async Task Menu(Ctx c)
    {
        var apps = c.App.Cfg.Apps;
        var rows = new List<InlineKeyboardButton[]>();
        for (var i = 0; i < apps.Count; i += 2)
        {
            var row = new List<InlineKeyboardButton> { B("▶️ " + Fmt.Cut(apps[i].Name, 18), $"a:r:{i}") };
            if (i + 1 < apps.Count)
                row.Add(B("▶️ " + Fmt.Cut(apps[i + 1].Name, 18), $"a:r:{i + 1}"));
            rows.Add(row.ToArray());
        }
        rows.Add(R(B("🔎 Найти и запустить", "a:s"), B("📋 Процессы", "a:ps")));
        rows.Add(R(B("➕ Добавить", "a:add"), B("🗑 Убрать", "a:del")));
        rows.Add(BackMenu);
        var text = apps.Count == 0 ? "🚀 <b>Программы</b>\nСписок избранного пуст. Добавьте программы или найдите нужную." : "🚀 <b>Программы</b>\nНажмите на программу, чтобы запустить её на ПК.";
        await c.Show(text, new InlineKeyboardMarkup(rows));
        await c.Toast();
    }

    private static async Task Run(Ctx c, string arg)
    {
        var apps = c.App.Cfg.Apps;
        if (!int.TryParse(arg, out var i) || i < 0 || i >= apps.Count)
        {
            await c.Toast("Программа не найдена", true);
            return;
        }
        var app = apps[i];
        await Launch(c, app.Name, app.Path, app.Args);
    }

    private static async Task RunFound(Ctx c, string arg)
    {
        (string Name, string Path, string Args)? item = null;
        lock (Sync)
        {
            if (Found.TryGetValue(c.User.Id, out var list) && int.TryParse(arg, out var i) && i >= 0 && i < list.Count)
                item = list[i];
        }
        if (item == null)
        {
            await c.Toast("Результаты поиска устарели, найдите заново", true);
            return;
        }
        await Launch(c, item.Value.Name, item.Value.Path, item.Value.Args);
    }

    private static async Task AddFound(Ctx c, string arg)
    {
        (string Name, string Path, string Args)? item = null;
        lock (Sync)
        {
            if (Found.TryGetValue(c.User.Id, out var list) && int.TryParse(arg, out var i) && i >= 0 && i < list.Count)
                item = list[i];
        }
        if (item == null)
        {
            await c.Toast("Результаты поиска устарели, найдите заново", true);
            return;
        }
        AddApp(c.App.Cfg, item.Value.Name, item.Value.Path, item.Value.Args);
        await c.Audit("Программа добавлена в избранное: " + item.Value.Name);
        await Menu(c);
    }

    private static async Task Launch(Ctx c, string name, string path, string args)
    {
        await c.Toast("Запускаю...");
        await c.App.Agent.Call("launch", new JsonObject { ["path"] = path, ["args"] = args }, 20);
        await c.Audit("Запуск программы: " + name);
        await c.Show($"✅ <b>{Fmt.H(name)}</b> запущена на ПК.", Kb(
            R(B("📸 Скриншот", "ss"), B("🚀 Программы", "a")),
            BackMenu));
    }

    private static async Task DeleteList(Ctx c)
    {
        var apps = c.App.Cfg.Apps;
        var rows = new List<InlineKeyboardButton[]>();
        for (var i = 0; i < apps.Count; i++)
            rows.Add(R(B("🗑 " + Fmt.Cut(apps[i].Name, 40), $"a:dy:{i}")));
        rows.Add(R(B("⬅️ Назад", "a")));
        await c.Show("🗑 <b>Какую программу убрать из списка?</b>\nСама программа с ПК не удаляется.", new InlineKeyboardMarkup(rows));
        await c.Toast();
    }

    private static async Task DeleteApp(Ctx c, string arg)
    {
        var apps = c.App.Cfg.Apps;
        if (int.TryParse(arg, out var i) && i >= 0 && i < apps.Count)
        {
            var name = apps[i].Name;
            apps.RemoveAt(i);
            c.App.Cfg.Save();
            await c.Audit("Программа убрана из избранного: " + name);
        }
        await Menu(c);
    }

    // Storage

    private static void AddApp(Config cfg, string name, string path, string args = "")
    {
        if (cfg.Apps.Any(x => string.Equals(x.Path, path, StringComparison.OrdinalIgnoreCase) && string.Equals(x.Args, args, StringComparison.OrdinalIgnoreCase)))
            return;
        cfg.Apps.Add(new AppEntry { Name = name, Path = path, Args = args, Process = Path.GetFileNameWithoutExtension(path) });
        cfg.Save();
    }

    private static void EnsureSeeded(Config cfg)
    {
        if (cfg.AppsSeeded)
            return;
        var session = Native.WTSGetActiveConsoleSessionId();
        var user = session == 0xFFFFFFFF ? null : Native.SessionUser(session);
        var profile = user == null ? null : Path.Combine(Environment.GetEnvironmentVariable("SystemDrive") + "\\", "Users", user);
        var pf = Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles);
        var pf86 = Environment.GetFolderPath(Environment.SpecialFolder.ProgramFilesX86);
        var candidates = new List<(string Name, string Path, string Args)>
        {
            ("AnyDesk", cfg.AnyDeskPath, ""),
            ("Steam", Path.Combine(pf86, "Steam", "steam.exe"), ""),
            ("Chrome", Path.Combine(pf, "Google", "Chrome", "Application", "chrome.exe"), ""),
            ("Edge", Path.Combine(pf86, "Microsoft", "Edge", "Application", "msedge.exe"), ""),
            ("Firefox", Path.Combine(pf, "Mozilla Firefox", "firefox.exe"), ""),
        };
        if (profile != null)
        {
            candidates.Add(("Telegram", Path.Combine(profile, "AppData", "Roaming", "Telegram Desktop", "Telegram.exe"), ""));
            candidates.Add(("Discord", Path.Combine(profile, "AppData", "Local", "Discord", "Update.exe"), "--processStart Discord.exe"));
        }
        foreach (var (name, path, args) in candidates)
        {
            if (File.Exists(path))
                cfg.Apps.Add(new AppEntry { Name = name, Path = path, Args = args, Process = Path.GetFileNameWithoutExtension(path) });
        }
        if (profile != null)
            cfg.AppsSeeded = true;
        cfg.Save();
    }

    // Start menu search

    private static async Task<List<(string Name, string Path, string Args)>> Search(BotApp app, string query)
    {
        var terms = Terms(query);
        try
        {
            var arr = new JsonArray();
            foreach (var t in terms)
                arr.Add(t);
            var res = await app.Agent.Call("apps_search", new JsonObject { ["terms"] = arr }, 40);
            return res["apps"]!.AsArray()
                .Select(x => (x!["name"]!.GetValue<string>(), "explorer.exe", "shell:AppsFolder\\" + x["app_id"]!.GetValue<string>()))
                .ToList();
        }
        catch (AgentException ex)
        {
            Log.Warn("apps search via agent failed, using shortcuts: " + ex.Message);
            return SearchLinks(terms).Select(r => (r.Name, r.Path, "")).ToList();
        }
    }

    private static List<(string Name, string Path)> SearchLinks(List<string> terms)
    {
        var query = terms[0];
        var roots = new List<string>
        {
            Path.Combine(Environment.GetFolderPath(Environment.SpecialFolder.CommonApplicationData), "Microsoft", "Windows", "Start Menu", "Programs"),
        };
        var session = Native.WTSGetActiveConsoleSessionId();
        var user = session == 0xFFFFFFFF ? null : Native.SessionUser(session);
        if (user != null)
            roots.Add(Path.Combine(Environment.GetEnvironmentVariable("SystemDrive") + "\\", "Users", user, "AppData", "Roaming", "Microsoft", "Windows", "Start Menu", "Programs"));
        var results = new List<(string Name, string Path)>();
        foreach (var root in roots.Where(Directory.Exists))
        {
            IEnumerable<string> files;
            try
            {
                files = Directory.EnumerateFiles(root, "*.lnk", SearchOption.AllDirectories);
            }
            catch
            {
                continue;
            }
            foreach (var f in files)
            {
                var name = Path.GetFileNameWithoutExtension(f);
                if (terms.Any(t => name.Contains(t, StringComparison.OrdinalIgnoreCase)) && !name.Contains("uninstall", StringComparison.OrdinalIgnoreCase)
                    && !results.Any(r => r.Name.Equals(name, StringComparison.OrdinalIgnoreCase)))
                    results.Add((name, f));
            }
        }
        return results.OrderBy(r => r.Name.StartsWith(query, StringComparison.OrdinalIgnoreCase) ? 0 : 1).ThenBy(r => r.Name.Length).Take(8).ToList();
    }
}
