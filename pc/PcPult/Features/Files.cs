using System.Text;
using Telegram.Bot;
using Telegram.Bot.Types;
using Telegram.Bot.Types.Enums;
using Telegram.Bot.Types.ReplyMarkups;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Files
{
    public const long SendLimit = 49L * 1024 * 1024;
    public const long ReceiveLimit = 20L * 1024 * 1024;
    private const int PageSize = 10;
    private const int MaxParts = 20;
    private const int MaxEntries = 3000;

    public static async Task Handle(Ctx c, string[] a)
    {
        if (!await c.Require("files"))
            return;
        var act = a.Length > 1 ? a[1] : "";
        var arg = a.Length > 2 ? a[2] : "";
        switch (act)
        {
            case "":
                await Roots(c);
                break;
            case "o":
                await OpenFolder(c, arg, a.Length > 3 && int.TryParse(a[3], out var page) ? page : 0);
                break;
            case "i":
                await Card(c, arg);
                break;
            case "d":
                await Download(c, arg);
                break;
            case "ds":
                await DownloadParts(c, arg);
                break;
            case "x":
                await ConfirmDelete(c, arg);
                break;
            case "xy":
                await Delete(c, arg);
                break;
        }
    }

    // Roots

    private static string? UserProfile()
    {
        var session = Native.WTSGetActiveConsoleSessionId();
        var user = session == 0xFFFFFFFF ? null : Native.SessionUser(session);
        return user == null ? null : Path.Combine(Environment.GetEnvironmentVariable("SystemDrive") + "\\", "Users", user);
    }

    private static readonly Lock FoldersLock = new();
    private static Dictionary<string, string> _folders = [];
    private static DateTime _foldersAt = DateTime.MinValue;

    private static async Task<Dictionary<string, string>> Folders(BotApp app)
    {
        lock (FoldersLock)
        {
            if (_folders.Count > 0 && DateTime.UtcNow - _foldersAt < TimeSpan.FromMinutes(2))
                return _folders;
        }
        var result = new Dictionary<string, string>();
        try
        {
            var res = await app.Agent.Call("folders", null, 8);
            foreach (var kv in res)
            {
                if (kv.Value?.GetValue<string>() is { Length: > 0 } p)
                    result[kv.Key] = p;
            }
        }
        catch (AgentException)
        {
            var profile = UserProfile();
            if (profile != null)
            {
                foreach (var (key, name) in new[] { ("desktop", "Desktop"), ("downloads", "Downloads"), ("documents", "Documents"), ("pictures", "Pictures"), ("videos", "Videos"), ("music", "Music") })
                    result[key] = Path.Combine(profile, name);
            }
        }
        lock (FoldersLock)
        {
            _folders = result;
            _foldersAt = DateTime.UtcNow;
        }
        return result;
    }

    public static async Task<string> UploadDir(BotApp app)
    {
        if (!string.IsNullOrWhiteSpace(app.Cfg.UploadDir))
            return app.Cfg.UploadDir;
        var folders = await Folders(app);
        return folders.TryGetValue("downloads", out var downloads)
            ? Path.Combine(downloads, "Upload")
            : Path.Combine(Config.Dir, "uploads");
    }

    private static async Task Roots(Ctx c)
    {
        var rows = new List<InlineKeyboardButton[]>();
        var folders = await Folders(c.App);
        var quick = new[] { ("🖥 Рабочий стол", "desktop"), ("⬇️ Загрузки", "downloads"), ("📄 Документы", "documents"), ("🖼 Изображения", "pictures"), ("🎬 Видео", "videos"), ("🎵 Музыка", "music") };
        var buttons = quick
            .Where(q => folders.TryGetValue(q.Item2, out var p) && Directory.Exists(p))
            .Select(q => B(q.Item1, "f:o:" + c.App.Paths.Id(folders[q.Item2])))
            .ToList();
        rows.AddRange(buttons.Chunk(2));
        var drives = DriveInfo.GetDrives().Where(d => d.IsReady && d.DriveType is DriveType.Fixed or DriveType.Removable or DriveType.Network)
            .Select(d => B($"💽 {d.Name.TrimEnd('\\')} · {Fmt.Size(d.AvailableFreeSpace)} своб.", "f:o:" + c.App.Paths.Id(d.RootDirectory.FullName)));
        rows.AddRange(drives.Chunk(2));
        rows.Add(R(B("⬆️ Папка загрузки в ПК", "f:o:" + c.App.Paths.Id(EnsureDir(await UploadDir(c.App))))));
        rows.Add(BackMenu);
        await c.Show("📁 <b>Файлы</b>\n\nВыберите папку. Чтобы положить файл на ПК, просто пришлите его боту (до 20 МБ), он сохранится в папку загрузки.", new InlineKeyboardMarkup(rows));
        await c.Toast();
    }

    private static string EnsureDir(string path)
    {
        try
        {
            Directory.CreateDirectory(path);
        }
        catch
        {
        }
        return path;
    }

    // Folder

    private sealed record Entry(string Path, string Name, bool IsDir, long Size);

    private static List<Entry> Read(string dir, out string? error)
    {
        error = null;
        var list = new List<Entry>();
        try
        {
            var info = new DirectoryInfo(dir);
            foreach (var d in info.EnumerateDirectories().Take(MaxEntries))
                list.Add(new Entry(d.FullName, d.Name, true, 0));
            foreach (var f in info.EnumerateFiles().Take(MaxEntries))
                list.Add(new Entry(f.FullName, f.Name, false, f.Length));
        }
        catch (Exception ex)
        {
            error = ex.Message;
        }
        return list
            .OrderByDescending(e => e.IsDir)
            .ThenBy(e => e.Name, StringComparer.CurrentCultureIgnoreCase)
            .ToList();
    }

    private static async Task OpenFolder(Ctx c, string id, int page)
    {
        var dir = c.App.Paths.Path(id);
        if (dir == null || !Directory.Exists(dir))
        {
            await c.Toast("Папка не найдена, откройте список заново", true);
            await Roots(c);
            return;
        }
        var entries = Read(dir, out var error);
        var pages = Math.Max(1, (entries.Count + PageSize - 1) / PageSize);
        page = Math.Clamp(page, 0, pages - 1);
        var shown = entries.Skip(page * PageSize).Take(PageSize).ToList();

        var sb = new StringBuilder($"📁 <b>{Fmt.H(Fmt.Cut(dir, 120))}</b>\n");
        var dirs = entries.Count(e => e.IsDir);
        sb.Append(error != null ? $"\n⚠️ {Fmt.H(Fmt.Cut(error, 150))}" : $"\n{dirs} {Fmt.Plural(dirs, "папка", "папки", "папок")}, {entries.Count - dirs} {Fmt.Plural(entries.Count - dirs, "файл", "файла", "файлов")}");
        if (pages > 1)
            sb.Append($" · страница {page + 1} из {pages}");
        if (entries.Count == 0 && error == null)
            sb.Append("\nПапка пуста.");

        var rows = new List<InlineKeyboardButton[]>();
        foreach (var e in shown)
        {
            var eid = c.App.Paths.Id(e.Path);
            rows.Add(e.IsDir
                ? R(B("📁 " + Fmt.Cut(e.Name, 40), "f:o:" + eid))
                : R(B($"📄 {Fmt.Cut(e.Name, 32)} · {Fmt.Size(e.Size)}", "f:i:" + eid)));
        }
        if (pages > 1)
        {
            rows.Add(R(
                B(page > 0 ? "⬅️" : "·", page > 0 ? $"f:o:{id}:{page - 1}" : "x"),
                B($"{page + 1}/{pages}", "x"),
                B(page < pages - 1 ? "➡️" : "·", page < pages - 1 ? $"f:o:{id}:{page + 1}" : "x")));
        }
        var parent = Directory.GetParent(dir);
        rows.Add(R(
            B("⬆️ Вверх", parent == null ? "f" : "f:o:" + c.App.Paths.Id(parent.FullName)),
            B("🏠 Начало", "f"),
            B("🔄", $"f:o:{id}:{page}")));
        rows.Add(BackMenu);
        await c.Show(sb.ToString(), new InlineKeyboardMarkup(rows));
        await c.Toast();
    }

    // File card

    private static async Task Card(Ctx c, string id)
    {
        var path = c.App.Paths.Path(id);
        if (path == null || !File.Exists(path))
        {
            await c.Toast("Файл не найден", true);
            return;
        }
        var fi = new FileInfo(path);
        var parentId = c.App.Paths.Id(fi.DirectoryName ?? Path.GetPathRoot(path)!);
        var sb = new StringBuilder($"📄 <b>{Fmt.H(fi.Name)}</b>\n\n");
        sb.AppendLine($"Папка: <code>{Fmt.H(Fmt.Cut(fi.DirectoryName ?? "", 150))}</code>");
        sb.AppendLine($"Размер: {Fmt.Size(fi.Length)}");
        sb.AppendLine($"Изменён: {Fmt.DateTimeShort(fi.LastWriteTime)}");
        sb.Append($"Создан: {Fmt.DateTimeShort(fi.CreationTime)}");
        var tooBig = fi.Length > SendLimit;
        if (tooBig)
            sb.Append($"\n\n⚠️ Файл больше 50 МБ, Telegram не примет его целиком. Его можно отправить частями.");
        await c.Show(sb.ToString(), Kb(
            R(tooBig ? B("⬇️ Отправить частями", "f:ds:" + id) : B("⬇️ Скачать", "f:d:" + id)),
            R(B("🗑 Удалить", "f:x:" + id)),
            R(B("⬅️ К папке", "f:o:" + parentId), B("🏠 Начало", "f"))));
        await c.Toast();
    }

    // Download

    private static async Task Download(Ctx c, string id)
    {
        var path = c.App.Paths.Path(id);
        if (path == null || !File.Exists(path))
        {
            await c.Toast("Файл не найден", true);
            return;
        }
        var fi = new FileInfo(path);
        if (fi.Length > SendLimit)
        {
            await Card(c, id);
            return;
        }
        await c.Toast("Отправляю...");
        await c.Bot.SendChatAction(c.ChatId, ChatAction.UploadDocument, cancellationToken: c.Ct);
        byte[] bytes;
        await using (var fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete))
        {
            bytes = new byte[fs.Length];
            await fs.ReadExactlyAsync(bytes, c.Ct);
        }
        await c.Audit("Скачан файл: " + path);
        var parentId = c.App.Paths.Id(fi.DirectoryName ?? Path.GetPathRoot(path)!);
        await c.ShowPhoto(bytes, fi.Name, $"📄 {Fmt.H(fi.Name)} · {Fmt.Size(fi.Length)}", Kb(
            R(B("⬅️ К папке", "f:o:" + parentId), B("🏠 Начало", "f"))), true);
    }

    private static async Task DownloadParts(Ctx c, string id)
    {
        var path = c.App.Paths.Path(id);
        if (path == null || !File.Exists(path))
        {
            await c.Toast("Файл не найден", true);
            return;
        }
        var fi = new FileInfo(path);
        var partSize = 45L * 1024 * 1024;
        var count = (int)((fi.Length + partSize - 1) / partSize);
        if (count > MaxParts)
        {
            await c.Toast($"Файл слишком большой: больше {MaxParts} частей по 45 МБ", true);
            return;
        }
        await c.Toast("Отправляю частями...");
        await c.Audit($"Скачан файл частями ({count}): {path}");
        await using var fs = new FileStream(path, FileMode.Open, FileAccess.Read, FileShare.ReadWrite | FileShare.Delete);
        for (var i = 0; i < count; i++)
        {
            await c.Bot.SendChatAction(c.ChatId, ChatAction.UploadDocument, cancellationToken: c.Ct);
            var length = (int)Math.Min(partSize, fi.Length - i * partSize);
            var buf = new byte[length];
            await fs.ReadExactlyAsync(buf, c.Ct);
            await using var ms = new MemoryStream(buf);
            await c.Bot.SendDocument(c.ChatId, InputFile.FromStream(ms, $"{fi.Name}.{i + 1:D3}"), $"Часть {i + 1} из {count}", cancellationToken: c.Ct);
        }
        var note = $"✅ Отправлено частей: {count}.\nСобрать на Windows: <code>copy /b \"{Fmt.H(fi.Name)}.001\"+\"{Fmt.H(fi.Name)}.002\"+... \"{Fmt.H(fi.Name)}\"</code> или распаковать 7-Zip, выбрав первую часть.";
        var parentId = c.App.Paths.Id(fi.DirectoryName ?? Path.GetPathRoot(path)!);
        c.MsgId = 0;
        await c.Show(note, Kb(R(B("⬅️ К папке", "f:o:" + parentId), B("🏠 Начало", "f"))));
    }

    // Delete

    public static bool IsProtected(string path)
    {
        string full;
        try
        {
            full = Path.GetFullPath(path).TrimEnd('\\');
        }
        catch
        {
            return true;
        }
        var denied = new[]
        {
            Environment.GetFolderPath(Environment.SpecialFolder.Windows),
            Environment.GetFolderPath(Environment.SpecialFolder.ProgramFiles),
            Environment.GetFolderPath(Environment.SpecialFolder.ProgramFilesX86),
            Config.Dir,
        };
        if (Path.GetPathRoot(full)?.TrimEnd('\\').Equals(full, StringComparison.OrdinalIgnoreCase) == true)
            return true;
        return denied.Where(d => !string.IsNullOrEmpty(d)).Any(d =>
            full.Equals(d.TrimEnd('\\'), StringComparison.OrdinalIgnoreCase) ||
            full.StartsWith(d.TrimEnd('\\') + "\\", StringComparison.OrdinalIgnoreCase));
    }

    private static async Task ConfirmDelete(Ctx c, string id)
    {
        var path = c.App.Paths.Path(id);
        if (path == null || !File.Exists(path))
        {
            await c.Toast("Файл не найден", true);
            return;
        }
        if (IsProtected(path))
        {
            await c.Toast("Системные файлы и файлы программы удалять нельзя", true);
            return;
        }
        await c.Show($"🗑 <b>Удалить файл?</b>\n<code>{Fmt.H(Fmt.Cut(path, 200))}</code>\n\nФайл удалится насовсем, минуя корзину.", Kb(
            R(B("✅ Удалить", "f:xy:" + id), B("Отмена", "f:i:" + id))));
        await c.Toast();
    }

    private static async Task Delete(Ctx c, string id)
    {
        var path = c.App.Paths.Path(id);
        if (path == null || !File.Exists(path) || IsProtected(path))
        {
            await c.Toast("Файл не найден или защищён", true);
            return;
        }
        try
        {
            File.Delete(path);
        }
        catch (Exception ex)
        {
            await c.Toast("Не получилось: " + Fmt.Cut(ex.Message, 120), true);
            return;
        }
        await c.Audit("Удалён файл: " + path);
        await c.Toast("Удалено");
        var parent = Path.GetDirectoryName(path) ?? Path.GetPathRoot(path)!;
        await OpenFolder(c, c.App.Paths.Id(parent), 0);
    }

    // Upload

    public static bool HasAttachment(Message m) =>
        m.Document != null || m.Photo is { Length: > 0 } || m.Video != null || m.Audio != null || m.Voice != null || m.VideoNote != null;

    public static string SanitizeName(string name)
    {
        var clean = new string(Path.GetFileName(name).Select(ch => Path.GetInvalidFileNameChars().Contains(ch) ? '_' : ch).ToArray()).Trim().Trim('.');
        return clean.Length == 0 ? "file" : Fmt.Cut(clean, 120).TrimEnd('…');
    }

    public static string UniquePath(string dir, string name)
    {
        var path = Path.Combine(dir, name);
        if (!File.Exists(path))
            return path;
        var stem = Path.GetFileNameWithoutExtension(name);
        var ext = Path.GetExtension(name);
        for (var i = 1; i < 1000; i++)
        {
            path = Path.Combine(dir, $"{stem} ({i}){ext}");
            if (!File.Exists(path))
                return path;
        }
        return Path.Combine(dir, $"{stem}-{Guid.NewGuid():N}{ext}");
    }

    public static async Task Upload(Ctx c, Message m)
    {
        try
        {
            await c.Bot.DeleteMessage(c.ChatId, m.Id, c.Ct);
        }
        catch
        {
        }
        if (!c.Has("files"))
        {
            await c.Show("🚫 Нет прав сохранять файлы на ПК.", Kb(BackMenu));
            return;
        }
        string fileId, name;
        long? size;
        var stamp = DateTime.Now.ToString("yyyyMMdd-HHmmss");
        if (m.Document is { } d)
            (fileId, name, size) = (d.FileId, d.FileName ?? $"file-{stamp}", d.FileSize);
        else if (m.Photo is { Length: > 0 } photos)
            (fileId, name, size) = (photos[^1].FileId, $"photo-{stamp}.jpg", photos[^1].FileSize);
        else if (m.Video is { } v)
            (fileId, name, size) = (v.FileId, v.FileName ?? $"video-{stamp}.mp4", v.FileSize);
        else if (m.Audio is { } au)
            (fileId, name, size) = (au.FileId, au.FileName ?? $"audio-{stamp}.mp3", au.FileSize);
        else if (m.Voice is { } vo)
            (fileId, name, size) = (vo.FileId, $"voice-{stamp}.ogg", vo.FileSize);
        else if (m.VideoNote is { } vn)
            (fileId, name, size) = (vn.FileId, $"videonote-{stamp}.mp4", vn.FileSize);
        else
            return;

        if (size > ReceiveLimit)
        {
            await c.Show($"⚠️ Файл {Fmt.Size(size.Value)}: Telegram разрешает боту принимать только файлы до 20 МБ.", Kb(BackMenu));
            return;
        }
        var dir = EnsureDir(await UploadDir(c.App));
        var target = UniquePath(dir, SanitizeName(name));
        var temp = target + ".part";
        try
        {
            var file = await c.Bot.GetFile(fileId, c.Ct);
            await using (var fs = new FileStream(temp, FileMode.Create, FileAccess.Write))
                await c.Bot.DownloadFile(file.FilePath!, fs, c.Ct);
            File.Move(temp, target);
        }
        catch (Exception ex)
        {
            try
            {
                File.Delete(temp);
            }
            catch
            {
            }
            Log.Error("upload", ex);
            await c.Show("⚠️ Не получилось сохранить файл: " + Fmt.H(Fmt.Cut(ex.Message, 150)), Kb(BackMenu));
            return;
        }
        var length = new FileInfo(target).Length;
        await c.Audit($"Загружен файл на ПК: {target}");
        await c.Show($"✅ <b>Файл сохранён</b>\n<code>{Fmt.H(target)}</code>\n{Fmt.Size(length)}", Kb(
            R(B("📁 Открыть папку", "f:o:" + c.App.Paths.Id(dir)), B("⬅️ Меню", "menu"))));
    }
}
