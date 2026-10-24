using PcPult.Shared;

namespace PcPult.Tests;

public class FmtTests
{
    [Theory]
    [InlineData(1, "день")]
    [InlineData(2, "дня")]
    [InlineData(5, "дней")]
    [InlineData(11, "дней")]
    [InlineData(21, "день")]
    [InlineData(112, "дней")]
    public void PluralPicksRussianForm(long n, string expected) =>
        Assert.Equal(expected, Fmt.Plural(n, "день", "дня", "дней"));

    [Theory]
    [InlineData(5, "5 с")]
    [InlineData(125, "2 мин")]
    [InlineData(3600, "1 ч")]
    [InlineData(5400, "1 ч 30 мин")]
    [InlineData(90000, "1 день 1 ч")]
    [InlineData(172800, "2 дня")]
    public void DurIsReadable(int seconds, string expected) =>
        Assert.Equal(expected, Fmt.Dur(TimeSpan.FromSeconds(seconds)));

    [Fact]
    public void DurNeverNegative() => Assert.Equal("0 с", Fmt.Dur(TimeSpan.FromSeconds(-9)));

    [Theory]
    [InlineData(512, "512 Б")]
    [InlineData(2048, "2,0 КБ")]
    [InlineData(5L * 1024 * 1024 * 1024, "5,0 ГБ")]
    public void SizeUsesUnits(long bytes, string expected) =>
        Assert.Equal(expected, Fmt.Size(bytes));

    [Fact]
    public void CutAddsEllipsis()
    {
        Assert.Equal("abcd…", Fmt.Cut("abcdefgh", 5));
        Assert.Equal("abc", Fmt.Cut("abc", 5));
    }

    [Fact]
    public void HtmlIsEscaped() => Assert.Equal("a &lt;b&gt; &amp; c", Fmt.H("a <b> & c"));
}
