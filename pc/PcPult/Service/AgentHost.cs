using System.Diagnostics;
using System.IO.Pipes;
using System.Runtime.InteropServices;
using System.Security.AccessControl;
using System.Security.Principal;
using System.Text;
using System.Text.Json.Nodes;
using PcPult.Shared;

namespace PcPult.Service;

public sealed class AgentHost : IDisposable
{
    private readonly BotApp _app;
    private readonly CancellationTokenSource _cts = new();
    private volatile PipeChannel? _channel;
    private DateTime _launchedAt = DateTime.MinValue;

    public string? User { get; private set; }

    public AgentHost(BotApp app) => _app = app;

    public bool Available => _channel != null;

    public void Start()
    {
        _ = Task.Run(AcceptLoop);
        _ = Task.Run(LaunchLoop);
    }

    public Task<JsonObject> Call(string op, JsonObject? args = null, int timeoutSec = 20, CancellationToken ct = default)
    {
        var ch = _channel ?? throw new AgentException(NoAgentMessage());
        return ch.Call(op, args, TimeSpan.FromSeconds(timeoutSec), ct);
    }

    private static string NoAgentMessage()
    {
        var session = Native.WTSGetActiveConsoleSessionId();
        return session == 0xFFFFFFFF || Native.SessionUser(session) == null
            ? "ПК на экране входа: в Windows никто не вошёл"
            : "Помощник в сессии пользователя ещё запускается, повторите через несколько секунд";
    }

    // Pipe server

    private static PipeSecurity BuildSecurity()
    {
        var ps = new PipeSecurity();
        ps.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.LocalSystemSid, null), PipeAccessRights.FullControl, AccessControlType.Allow));
        ps.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.BuiltinAdministratorsSid, null), PipeAccessRights.FullControl, AccessControlType.Allow));
        ps.AddAccessRule(new PipeAccessRule(new SecurityIdentifier(WellKnownSidType.AuthenticatedUserSid, null), PipeAccessRights.ReadWrite, AccessControlType.Allow));
        return ps;
    }

    private async Task AcceptLoop()
    {
        var ct = _cts.Token;
        while (!ct.IsCancellationRequested)
        {
            NamedPipeServerStream? server = null;
            try
            {
                server = NamedPipeServerStreamAcl.Create(PipeChannel.Name, PipeDirection.InOut, NamedPipeServerStream.MaxAllowedServerInstances,
                    PipeTransmissionMode.Byte, PipeOptions.Asynchronous, 1 << 16, 1 << 16, BuildSecurity());
                await server.WaitForConnectionAsync(ct);
                if (!IsTrustedClient(server, out var reason))
                {
                    Log.Warn("pipe client rejected: " + reason);
                    server.Dispose();
                    continue;
                }
                var old = _channel;
                var ch = new PipeChannel(server);
                ch.OnEvent = (op, args) =>
                {
                    if (op == "hello")
                    {
                        User = args["user"]?.GetValue<string>();
                        Log.Info($"agent connected: {User}");
                    }
                };
                _channel = ch;
                old?.Dispose();
                _ = RunChannel(ch);
            }
            catch (OperationCanceledException)
            {
                server?.Dispose();
                break;
            }
            catch (Exception ex)
            {
                server?.Dispose();
                Log.Error("pipe accept", ex);
                await Task.Delay(2000);
            }
        }
    }

    private async Task RunChannel(PipeChannel ch)
    {
        try
        {
            await ch.RunAsync(_cts.Token);
        }
        catch (Exception ex)
        {
            Log.Warn("agent channel: " + ex.Message);
        }
        finally
        {
            if (ReferenceEquals(_channel, ch))
            {
                _channel = null;
                User = null;
                Log.Info("agent disconnected");
            }
            ch.Dispose();
        }
    }

    private static bool IsTrustedClient(NamedPipeServerStream pipe, out string reason)
    {
        reason = "";
        if (!Native.GetNamedPipeClientProcessId(pipe.SafePipeHandle.DangerousGetHandle(), out var pid))
        {
            reason = "no client pid";
            return false;
        }
        try
        {
            using var p = Process.GetProcessById((int)pid);
            var path = p.MainModule?.FileName ?? "";
            var ours = Environment.ProcessPath ?? "";
            if (!string.Equals(path, ours, StringComparison.OrdinalIgnoreCase))
            {
                reason = $"unexpected image {path}";
                return false;
            }
            var active = Native.WTSGetActiveConsoleSessionId();
            if ((uint)p.SessionId != active)
            {
                reason = $"session {p.SessionId} is not the active one";
                return false;
            }
            return true;
        }
        catch (Exception ex)
        {
            reason = ex.Message;
            return false;
        }
    }

    // Launcher

    private async Task LaunchLoop()
    {
        var ct = _cts.Token;
        while (!ct.IsCancellationRequested)
        {
            try
            {
                LaunchIfNeeded();
                await Task.Delay(1500, ct);
            }
            catch (OperationCanceledException)
            {
                break;
            }
            catch (Exception ex)
            {
                Log.Error("agent launch", ex);
                try
                {
                    await Task.Delay(3000, ct);
                }
                catch (OperationCanceledException)
                {
                    break;
                }
            }
        }
    }

    private void LaunchIfNeeded()
    {
        if (_channel != null || DateTime.UtcNow - _launchedAt < TimeSpan.FromSeconds(20))
            return;
        var session = Native.WTSGetActiveConsoleSessionId();
        if (session == 0xFFFFFFFF || Native.SessionUser(session) == null)
            return;
        _launchedAt = DateTime.UtcNow;
        StartInSession(session);
    }

    private static void StartInSession(uint session)
    {
        var exe = Environment.ProcessPath ?? throw new InvalidOperationException("no process path");
        if (!Native.WTSQueryUserToken(session, out var userToken))
            throw new InvalidOperationException($"WTSQueryUserToken failed: {Marshal.GetLastWin32Error()}");
        IntPtr primary = IntPtr.Zero, env = IntPtr.Zero;
        try
        {
            if (!Native.DuplicateTokenEx(userToken, Native.MaximumAllowed, IntPtr.Zero, Native.SecurityImpersonation, Native.TokenPrimary, out primary))
                throw new InvalidOperationException($"DuplicateTokenEx failed: {Marshal.GetLastWin32Error()}");
            Native.CreateEnvironmentBlock(out env, primary, false);
            var si = new Native.StartupInfo { lpDesktop = @"winsta0\default" };
            si.cb = Marshal.SizeOf(si);
            var cmd = new StringBuilder($"\"{exe}\" --agent");
            if (!Native.CreateProcessAsUser(primary, null, cmd, IntPtr.Zero, IntPtr.Zero, false, Native.CreateUnicodeEnvironment, env,
                    Path.GetDirectoryName(exe), ref si, out var pi))
                throw new InvalidOperationException($"CreateProcessAsUser failed: {Marshal.GetLastWin32Error()}");
            Native.CloseHandle(pi.hThread);
            Native.CloseHandle(pi.hProcess);
            Log.Info($"agent started in session {session}, pid {pi.dwProcessId}");
        }
        finally
        {
            if (env != IntPtr.Zero)
                Native.DestroyEnvironmentBlock(env);
            if (primary != IntPtr.Zero)
                Native.CloseHandle(primary);
            Native.CloseHandle(userToken);
        }
    }

    public void Dispose()
    {
        _cts.Cancel();
        _channel?.Dispose();
    }
}
