using System.Diagnostics;
using System.Text;
using Telegram.Bot.Types.ReplyMarkups;
using PcPult.Service;
using PcPult.Shared;
using static PcPult.Service.Ui;

namespace PcPult.Features;

public static class Processes
{
    private static readonly HashSet<string> Protected = new(StringComparer.OrdinalIgnoreCase)
    {
        "System", "Idle", "Registry", "smss", "csrss", "wininit", "services", "lsass", "winlogon", "svchost", "dwm", "fontdrvhost",
        "Memory Compression", "MsMpEng", "PcPult", "explorer",
    };

    private sealed record Row(int Pid, string Name, double Cpu, long Memory);

    public static async Task Handle(Ctx c, string[] a)
    {
        var act = a[1];
        var arg = a.Length > 2 ? a[2] : "";
        switch (act)
        {
            case "ps":
                await List(c, null);
                break;
            case "pss":
                c.App.Pending.Set(c.User.Id, "procsearch", c.MsgId.ToString());
                await c.Show("🔎 <b>Найти процесс</b>\nНапишите часть имени процесса, например <code>chrome</code>.", Kb(R(B("Отмена", "a:ps"))));
                await c.Toast();
                break;
            case "k":
                await ConfirmKill(c, arg);
                break;
            case "ky":
                await Kill(c, arg);
                break;
        }
    }

    public static async Task Input(Ctx c, string text)
    {
        if (!c.Has("apps"))
            return;
        await List(c, text.Trim());
    }

    private static async Task<List<Row>> Sample()
    {
        var first = new Dictionary<int, (TimeSpan Cpu, string Name, long Mem)>();
        foreach (var p in Process.GetProcesses())
        {
            try
            {
                first[p.Id] = (p.TotalProcessorTime, p.ProcessName, p.WorkingSet64);
            }
            catch
            {
            }
            finally
            {
                p.Dispose();
            }
        }
        var started = Stopwatch.GetTimestamp();
        await Task.Delay(800);
        var elapsed = Stopwatch.GetElapsedTime(started).TotalSeconds;
        var rows = new List<Row>();
        foreach (var p in Process.GetProcesses())
        {
            try
            {
                if (first.TryGetValue(p.Id, out var f))
                {
                    var cpu = (p.TotalProcessorTime - f.Cpu).TotalSeconds / elapsed / Environment.ProcessorCount * 100;
                    rows.Add(new Row(p.Id, f.Name, Math.Max(0, cpu), p.WorkingSet64));
                }
            }
            catch
            {
            }
            finally
            {
                p.Dispose();
            }
        }
        return rows;
    }

    private static async Task List(Ctx c, string? filter)
    {
        await c.Toast();
        var rows = await Sample();
        IEnumerable<Row> list;
        string title;
        if (string.IsNullOrEmpty(filter))
        {
            var grouped = rows.GroupBy(r => r.Name)
                .Select(g => new Row(g.OrderByDescending(x => x.Memory).First().Pid, g.Key, g.Sum(x => x.Cpu), g.Sum(x => x.Memory)))
                .Where(r => !Protected.Contains(r.Name));
            list = grouped.OrderByDescending(r => r.Cpu * 3 + r.Memory / 1e9).Take(10);
            title = "📋 <b>Процессы</b> · по нагрузке";
        }
        else
        {
            list = rows.Where(r => r.Name.Contains(filter, StringComparison.OrdinalIgnoreCase)).OrderByDescending(r => r.Memory).Take(10);
            title = $"📋 <b>Процессы</b> · «{Fmt.H(filter)}»";
        }
        var shown = list.ToList();
        var sb = new StringBuilder(title).AppendLine();
        var buttons = new List<InlineKeyboardButton[]>();
        if (shown.Count == 0)
            sb.AppendLine("\nНичего не нашлось.");
        foreach (var r in shown)
        {
            sb.AppendLine($"\n<b>{Fmt.H(r.Name)}</b> · {r.Cpu:0.#}% · {Fmt.Size(r.Memory)}");
            if (!Protected.Contains(r.Name))
                buttons.Add(R(B($"❌ {Fmt.Cut(r.Name, 24)} ({Fmt.Size(r.Memory)})", $"a:k:{r.Pid}")));
        }
        buttons.Add(R(B("🔎 Найти", "a:pss"), B("🔄 Обновить", "a:ps")));
        buttons.Add(R(B("⬅️ Назад", "a")));
        await c.Show(sb.ToString(), new InlineKeyboardMarkup(buttons));
    }

    private static async Task ConfirmKill(Ctx c, string arg)
    {
        if (!c.Has("apps") || !int.TryParse(arg, out var pid))
            return;
        string name;
        try
        {
            using var p = Process.GetProcessById(pid);
            name = p.ProcessName;
        }
        catch
        {
            await c.Toast("Процесс уже закрыт", true);
            await List(c, null);
            return;
        }
        if (Protected.Contains(name))
        {
            await c.Toast("Этот системный процесс закрывать нельзя", true);
            return;
        }
        await c.Show($"❌ <b>Завершить {Fmt.H(name)}</b> (PID {pid}) вместе с дочерними процессами?\nНесохранённые данные в программе пропадут.", Kb(
            R(B("✅ Завершить", $"a:ky:{pid}"), B("Отмена", "a:ps"))));
        await c.Toast();
    }

    private static async Task Kill(Ctx c, string arg)
    {
        if (!c.Has("apps") || !int.TryParse(arg, out var pid))
            return;
        try
        {
            using var p = Process.GetProcessById(pid);
            var name = p.ProcessName;
            if (Protected.Contains(name))
            {
                await c.Toast("Этот системный процесс закрывать нельзя", true);
                return;
            }
            p.Kill(true);
            await c.Audit($"Завершён процесс {name} (PID {pid})");
            await c.Toast($"{name} завершён");
        }
        catch (Exception ex)
        {
            await c.Toast("Не получилось: " + Fmt.Cut(ex.Message, 120), true);
        }
        await List(c, null);
    }
}
