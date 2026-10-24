using System.Drawing;
using System.Drawing.Imaging;
using System.Text.Json.Nodes;
using System.Windows.Forms;

namespace PcPult.Agent;

public static class Screens
{
    private const int MaxPreviewWidth = 2560;

    public static JsonObject List()
    {
        var arr = new JsonArray();
        var i = 0;
        foreach (var s in Screen.AllScreens)
        {
            arr.Add(new JsonObject
            {
                ["index"] = i++,
                ["name"] = s.DeviceName.TrimStart('\\', '.'),
                ["width"] = s.Bounds.Width,
                ["height"] = s.Bounds.Height,
                ["primary"] = s.Primary,
            });
        }
        return new JsonObject { ["screens"] = arr };
    }

    public static JsonObject Capture(JsonObject a)
    {
        var index = a["monitor"]?.GetValue<int>() ?? -1;
        var original = a["original"]?.GetValue<bool>() ?? false;
        var screens = Screen.AllScreens;
        Rectangle bounds;
        if (index >= 0 && index < screens.Length)
        {
            bounds = screens[index].Bounds;
        }
        else
        {
            bounds = screens[0].Bounds;
            foreach (var s in screens.Skip(1))
                bounds = Rectangle.Union(bounds, s.Bounds);
        }
        using var bmp = new Bitmap(bounds.Width, bounds.Height, PixelFormat.Format32bppArgb);
        using (var g = Graphics.FromImage(bmp))
            g.CopyFromScreen(bounds.Location, Point.Empty, bounds.Size, CopyPixelOperation.SourceCopy);

        using var ms = new MemoryStream();
        string ext;
        if (original)
        {
            bmp.Save(ms, ImageFormat.Png);
            ext = "png";
        }
        else
        {
            Bitmap src = bmp;
            Bitmap? scaled = null;
            if (bmp.Width > MaxPreviewWidth)
            {
                var h = (int)((long)bmp.Height * MaxPreviewWidth / bmp.Width);
                scaled = new Bitmap(MaxPreviewWidth, h);
                using var g = Graphics.FromImage(scaled);
                g.InterpolationMode = System.Drawing.Drawing2D.InterpolationMode.HighQualityBicubic;
                g.DrawImage(bmp, 0, 0, MaxPreviewWidth, h);
                src = scaled;
            }
            var codec = ImageCodecInfo.GetImageEncoders().First(e => e.FormatID == ImageFormat.Jpeg.Guid);
            using var ep = new EncoderParameters(1);
            ep.Param[0] = new EncoderParameter(System.Drawing.Imaging.Encoder.Quality, 82L);
            src.Save(ms, codec, ep);
            scaled?.Dispose();
            ext = "jpg";
        }
        return new JsonObject
        {
            ["data"] = Convert.ToBase64String(ms.ToArray()),
            ["ext"] = ext,
            ["width"] = bounds.Width,
            ["height"] = bounds.Height,
        };
    }
}
