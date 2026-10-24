using System.Drawing;
using System.Text.Json.Nodes;
using System.Windows.Forms;

namespace PcPult.Agent;

public static class Popup
{
    public static JsonObject Show(JsonObject a)
    {
        var title = a["title"]?.GetValue<string>() ?? "Сообщение";
        var text = a["text"]?.GetValue<string>() ?? "";
        var seconds = Math.Clamp(a["seconds"]?.GetValue<int>() ?? 0, 0, 3600);

        var form = new Form
        {
            Text = title,
            TopMost = true,
            StartPosition = FormStartPosition.CenterScreen,
            FormBorderStyle = FormBorderStyle.FixedDialog,
            MaximizeBox = false,
            MinimizeBox = false,
            ShowInTaskbar = true,
            ClientSize = new Size(560, 240),
            BackColor = Color.FromArgb(32, 32, 36),
            ForeColor = Color.White,
            Font = new Font("Segoe UI", 12f),
        };
        var label = new Label
        {
            Text = text,
            Dock = DockStyle.Fill,
            Padding = new Padding(24, 24, 24, 8),
            AutoEllipsis = true,
        };
        var ok = new Button
        {
            Text = "Закрыть",
            Dock = DockStyle.Bottom,
            Height = 44,
            FlatStyle = FlatStyle.Flat,
            BackColor = Color.FromArgb(0, 120, 212),
            ForeColor = Color.White,
        };
        ok.FlatAppearance.BorderSize = 0;
        ok.Click += (_, _) => form.Close();
        form.Controls.Add(label);
        form.Controls.Add(ok);
        form.AcceptButton = ok;
        if (seconds > 0)
        {
            var timer = new System.Windows.Forms.Timer { Interval = seconds * 1000 };
            timer.Tick += (_, _) =>
            {
                timer.Stop();
                form.Close();
            };
            form.FormClosed += (_, _) => timer.Dispose();
            timer.Start();
        }
        form.Show();
        PcPult.Shared.Native.BringToFront(form.Handle);
        form.Activate();
        return [];
    }
}
