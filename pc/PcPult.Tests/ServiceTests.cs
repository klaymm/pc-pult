using PcPult.Service;
using PcPult.Shared;

namespace PcPult.Tests;

public class PendingTests
{
    [Fact]
    public void TakeReturnsOnceAndClears()
    {
        var p = new Pending();
        p.Set(1, "popup", "42");
        Assert.True(p.Has(1));
        var taken = p.Take(1);
        Assert.Equal(("popup", "42"), taken);
        Assert.Null(p.Take(1));
        Assert.False(p.Has(1));
    }

    [Fact]
    public void UsersAreIndependent()
    {
        var p = new Pending();
        p.Set(1, "say");
        Assert.Null(p.Take(2));
        Assert.NotNull(p.Take(1));
    }

    [Fact]
    public void ClearDropsPending()
    {
        var p = new Pending();
        p.Set(1, "say");
        p.Clear(1);
        Assert.Null(p.Take(1));
    }
}

public class BackoffTests
{
    [Fact]
    public void GrowsAndStopsAtFiveSeconds()
    {
        var d = Backoff.First;
        var seen = new List<double>();
        for (var i = 0; i < 6; i++)
        {
            seen.Add(d.TotalMilliseconds);
            d = Backoff.Next(d);
        }
        Assert.Equal([500d, 1000d, 2000d, 4000d, 5000d, 5000d], seen);
    }
}

public class UserInfoTests
{
    [Fact]
    public void OwnerHasEverything() => Assert.True(new UserInfo { Owner = true }.Has("clipboard"));

    [Fact]
    public void GuestOnlyHasListedPerms()
    {
        var u = new UserInfo { Perms = ["status", "power_on"] };
        Assert.True(u.Has("status"));
        Assert.False(u.Has("power"));
    }
}

public class PollItemTests
{
    [Fact]
    public void TelegramItemIsParsed()
    {
        var options = new System.Text.Json.JsonSerializerOptions { PropertyNamingPolicy = System.Text.Json.JsonNamingPolicy.SnakeCaseLower };
        var item = System.Text.Json.JsonSerializer.Deserialize<PollItem>("{\"update\":{\"update_id\":1},\"user\":{\"id\":7},\"panel\":3}", options)!;
        Assert.Equal(7, item.User.Id);
        Assert.Equal(3, item.Panel);
    }
}

public class ConfigTests
{
    [Fact]
    public void JsonUsesSnakeCaseAndRoundTrips()
    {
        var cfg = new Config { Key = "k", RouterUrl = "http://192.168.1.1:8765", Apps = [new AppEntry { Name = "A", Path = "C:\\a.exe" }] };
        var json = System.Text.Json.JsonSerializer.Serialize(cfg, Config.Json);
        Assert.Contains("\"router_url\"", json);
        Assert.Contains("\"cpu_temp_alert\"", json);
        var back = System.Text.Json.JsonSerializer.Deserialize<Config>(json, Config.Json)!;
        Assert.Equal("k", back.Key);
        Assert.Equal("C:\\a.exe", back.Apps[0].Path);
    }
}
