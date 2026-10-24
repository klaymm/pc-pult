using System.Diagnostics;
using System.Security.Cryptography;
using System.Text;
using System.Text.Json.Nodes;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class AnyDesk
{
    public static readonly HashSet<string> InputKinds = ["adpw"];

    private const string ServiceName = "AnyDesk";
    private const int PasswordShowSeconds = 60;

    public static async Task Handle(Ctx c, string[] a)
    {
        if (!await c.Require("anydesk"))
            return;
        var act = a.Length > 1 ? a[1] : "";
        switch (act)
        {
            case "":
                await View(c);
                break;
            case "open":
                await OpenWindow(c);
                break;
            case "svc":
                if (await c.Require("anydesk_admin"))
                    await RestartService(c);
                break;
            case "pw":
                if (await c.Require("anydesk_admin"))
                    await PasswordMenu(c);
                break;
            case "pwr":
                if (await c.Require("anydesk_admin"))
                    await SetPassword(c, GeneratePassword());
                break;
            case "pwc":
                if (await c.Require("anydesk_admin"))
                {
                    c.App.Pending.Set(c.User.Id, "adpw", c.MsgId.ToString());
                    await c.Show("✍️ <b>Свой пароль</b>\nПришлите новый пароль для доступа без подтверждения (от 8 символов). Ваше сообщение с паролем бот сразу удалит.", Kb(R(B("Отмена", "ad"))));
                    await c.Toast();
                }
                break;
        }
    }

    public static async Task Input(Ctx c, string kind, string text)
    {
        if (kind != "adpw" || !c.Has("anydesk_admin"))
            return;
        var pwd = text.Trim();
        if (pwd.Length < 8 || pwd.Any(char.IsControl))
        {
            c.App.Pending.Set(c.User.Id, "adpw", c.MsgId.ToString());
            await c.Show("Пароль слишком короткий. Нужно не меньше 8 символов, пришлите ещё раз.", Kb(R(B("Отмена", "ad"))));
            return;
        }
        await SetPassword(c, pwd);
    }

    private static string Exe(BotApp app) => app.Cfg.AnyDeskPath;

    private static async Task<string> Cli(BotApp app, string args, int timeoutSec = 15)
    {
        try
        {
            var (code, output) = await BotApp.RunProcess(Exe(app), args, timeoutSec);
            return code == 0 ? output : "";
        }
        catch
        {
            return "";
        }
    }

    private static async Task<string> ServiceState()
    {
        var (_, output) = await BotApp.RunProcess("sc.exe", $"query {ServiceName}", 10);
        if (output.Contains("RUNNING"))
            return "работает";
        if (output.Contains("STOPPED"))
            return "остановлена";
        if (output.Contains("1060"))
            return "не установлена";
        return "неизвестно";
    }

    private static string FormatId(string id)
    {
        var digits = new string(id.Where(char.IsDigit).ToArray());
        if (digits.Length < 6)
            return id;
        var groups = new List<string>();
        for (var end = digits.Length; end > 0; end -= 3)
            groups.Insert(0, digits[Math.Max(0, end - 3)..end]);
        return string.Join(' ', groups);
    }

    private static async Task View(Ctx c, string? note = null)
    {
        if (!File.Exists(Exe(c.App)))
        {
            await c.Show($"🖥 <b>AnyDesk</b>\nПрограмма не найдена по пути <code>{Fmt.H(Exe(c.App))}</code>. Путь задаётся в config.json (any_desk_path).", Kb(BackMenu));
            await c.Toast();
            return;
        }
        var idTask = Cli(c.App, "--get-id");
        var statusTask = Cli(c.App, "--get-status");
        var state = await ServiceState();
        var id = await idTask;
        var status = await statusTask;
        var window = Process.GetProcessesByName("AnyDesk").Any(p => p.SessionId != 0);
        var sb = new StringBuilder("🖥 <b>AnyDesk</b>\n\n");
        sb.AppendLine(string.IsNullOrWhiteSpace(id) ? "ID: не удалось получить" : $"ID: <code>{Fmt.H(FormatId(id))}</code>");
        if (!string.IsNullOrWhiteSpace(status))
            sb.AppendLine($"Сеть: {Fmt.H(status)}");
        sb.AppendLine($"Служба: {state}");
        sb.Append($"Окно программы: {(window ? "открыто" : "закрыто")}");
        if (note != null)
            sb.Append("\n\n" + note);
        await c.Show(sb.ToString(), Kb(
            R(B("▶️ Открыть окно", "ad:open"), B("🔄 Обновить", "ad")),
            If(c.Has("anydesk_admin"), B("🔑 Новый пароль", "ad:pw"), B("🔁 Перезапустить службу", "ad:svc")),
            BackMenu));
        await c.Toast();
    }

    private static async Task OpenWindow(Ctx c)
    {
        await c.Toast("Открываю...");
        await c.App.Agent.Call("launch", new JsonObject { ["path"] = Exe(c.App) }, 20);
        await c.Audit("Открыт AnyDesk");
        await Task.Delay(1500);
        await View(c);
    }

    private static async Task RestartService(Ctx c)
    {
        await c.Toast("Перезапускаю службу...");
        await BotApp.RunProcess("sc.exe", $"stop {ServiceName}", 20);
        for (var i = 0; i < 20; i++)
        {
            await Task.Delay(500);
            if (await ServiceState() == "остановлена")
                break;
        }
        await BotApp.RunProcess("sc.exe", $"start {ServiceName}", 20);
        await Task.Delay(2500);
        await c.Audit("Перезапуск службы AnyDesk");
        await View(c, "🔁 Служба перезапущена.");
    }

    private static async Task PasswordMenu(Ctx c)
    {
        await c.Show("🔑 <b>Новый пароль AnyDesk</b>\n\nПароль для доступа без подтверждения (неконтролируемый доступ) заменится на новый. Старый перестанет работать.", Kb(
            R(B("🎲 Случайный", "ad:pwr"), B("✍️ Свой", "ad:pwc")),
            R(B("Отмена", "ad"))));
        await c.Toast();
    }

    private static string GeneratePassword()
    {
        const string alphabet = "ABCDEFGHJKLMNPQRSTUVWXYZabcdefghijkmnpqrstuvwxyz23456789";
        var sb = new StringBuilder();
        for (var i = 0; i < 16; i++)
            sb.Append(alphabet[RandomNumberGenerator.GetInt32(alphabet.Length)]);
        return sb.ToString();
    }

    private static async Task SetPassword(Ctx c, string password)
    {
        await c.Toast("Задаю пароль...");
        var psi = new ProcessStartInfo(Exe(c.App), "--set-password")
        {
            UseShellExecute = false,
            CreateNoWindow = true,
            RedirectStandardInput = true,
            RedirectStandardOutput = true,
            RedirectStandardError = true,
        };
        string output;
        int code;
        using (var p = Process.Start(psi) ?? throw new InvalidOperationException("AnyDesk не запустился"))
        {
            await p.StandardInput.WriteLineAsync(password);
            p.StandardInput.Close();
            using var cts = new CancellationTokenSource(TimeSpan.FromSeconds(20));
            var o = p.StandardOutput.ReadToEndAsync(cts.Token);
            var e = p.StandardError.ReadToEndAsync(cts.Token);
            try
            {
                await p.WaitForExitAsync(cts.Token);
            }
            catch (OperationCanceledException)
            {
                p.Kill(true);
                await c.Show("⚠️ AnyDesk не ответил за 20 секунд. Пароль не изменён.", Kb(R(B("🔄 Повторить", "ad:pw"), B("⬅️ Назад", "ad"))));
                return;
            }
            output = (await o + await e).Trim();
            code = p.ExitCode;
        }
        if (code != 0)
        {
            await c.Audit("Не удалось сменить пароль AnyDesk");
            await c.Show($"⚠️ AnyDesk не принял пароль (код {code}).\n<pre>{Fmt.H(Fmt.Cut(output, 500))}</pre>", Kb(R(B("🔄 Повторить", "ad:pw"), B("⬅️ Назад", "ad"))));
            return;
        }
        await c.Audit("Сменён пароль AnyDesk");
        var nav = c.App.Nav(c.User.Id);
        await c.Show($"✅ <b>Пароль AnyDesk изменён</b>\n\n<code>{Fmt.H(password)}</code>\n\nЭто сообщение скроет пароль через {PasswordShowSeconds} секунд.", Kb(R(B("Скрыть сейчас", "ad"))));
        _ = Task.Run(async () =>
        {
            await Task.Delay(TimeSpan.FromSeconds(PasswordShowSeconds));
            if (c.App.Nav(c.User.Id) == nav)
            {
                try
                {
                    await View(c, "🔒 Пароль скрыт.");
                }
                catch (Exception ex)
                {
                    Log.Warn("hide password: " + ex.Message);
                }
            }
        });
    }
}
