using Microsoft.Extensions.DependencyInjection;
using Microsoft.Extensions.Hosting;
using PcPult.Service;
using PcPult.Shared;

namespace PcPult;

public static class Program
{
    public const string ServiceName = "PcPult";

    [STAThread]
    public static int Main(string[] args)
    {
        if (args.Contains("--agent"))
        {
            Log.Init("agent");
            try
            {
                Agent.AgentApp.Run();
            }
            catch (Exception ex)
            {
                Log.Error("agent crashed", ex);
                return 1;
            }
            return 0;
        }
        var builder = Host.CreateApplicationBuilder(args);
        builder.Services.AddWindowsService(o => o.ServiceName = ServiceName);
        builder.Services.AddHostedService<BotService>();
        builder.Services.Configure<HostOptions>(o => o.ShutdownTimeout = TimeSpan.FromSeconds(15));
        builder.Build().Run();
        return 0;
    }
}
