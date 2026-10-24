using PcPult.Features;
using PcPult.Service;

namespace PcPult.Tests;

public class PathIdsTests
{
    [Fact]
    public void SamePathGetsSameId()
    {
        var ids = new PathIds();
        var a = ids.Id(@"C:\Users\x");
        Assert.Equal(a, ids.Id(@"c:\users\X"));
        Assert.Equal(@"C:\Users\x", ids.Path(a));
    }

    [Fact]
    public void DifferentPathsGetDifferentShortIds()
    {
        var ids = new PathIds();
        var a = ids.Id(@"C:\a");
        var b = ids.Id(@"C:\b");
        Assert.NotEqual(a, b);
        Assert.True(a.Length < 10 && b.Length < 10);
    }

    [Fact]
    public void UnknownIdReturnsNull() => Assert.Null(new PathIds().Path("zzzz"));
}

public class FilesTests : IDisposable
{
    private readonly string _dir = Path.Combine(Path.GetTempPath(), "pcr-test-" + Guid.NewGuid().ToString("N"));

    public FilesTests() => Directory.CreateDirectory(_dir);

    public void Dispose() => Directory.Delete(_dir, true);

    [Theory]
    [InlineData(@"C:\Windows\System32\cmd.exe", true)]
    [InlineData(@"C:\Windows", true)]
    [InlineData(@"C:\Program Files\PcPult\PcPult.exe", true)]
    [InlineData(@"C:\ProgramData\PcPult\config.json", true)]
    [InlineData(@"C:\", true)]
    [InlineData(@"C:\Users\x\Downloads\a.txt", false)]
    [InlineData(@"F:\Games\save.dat", false)]
    public void ProtectedPathsAreRecognized(string path, bool expected) =>
        Assert.Equal(expected, Files.IsProtected(path));

    [Theory]
    [InlineData("report.pdf", "report.pdf")]
    [InlineData(@"..\..\evil.exe", "evil.exe")]
    [InlineData("a?b*c.txt", "a_b_c.txt")]
    [InlineData(@"C:\Users\x\evil.exe", "evil.exe")]
    [InlineData("...", "file")]
    [InlineData("", "file")]
    public void NamesAreSanitized(string input, string expected) =>
        Assert.Equal(expected, Files.SanitizeName(input));

    [Fact]
    public void UniquePathAddsCounter()
    {
        var first = Files.UniquePath(_dir, "a.txt");
        Assert.Equal(Path.Combine(_dir, "a.txt"), first);
        File.WriteAllText(first, "x");
        var second = Files.UniquePath(_dir, "a.txt");
        Assert.Equal(Path.Combine(_dir, "a (1).txt"), second);
        File.WriteAllText(second, "x");
        Assert.Equal(Path.Combine(_dir, "a (2).txt"), Files.UniquePath(_dir, "a.txt"));
    }
}
