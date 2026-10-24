using Telegram.Bot;
using Telegram.Bot.Exceptions;
using Telegram.Bot.Types;
using Telegram.Bot.Types.Enums;
using Telegram.Bot.Types.ReplyMarkups;
using PcPult.Shared;

namespace PcPult.Service;

public sealed class Ctx
{
    public required BotApp App { get; init; }
    public required UserInfo User { get; init; }
    public required long ChatId { get; init; }
    public int MsgId { get; set; }
    public int PanelId { get; set; }
    public bool MsgIsMedia { get; set; }
    public string? CallbackId { get; init; }
    public Message? Message { get; init; }
    public CancellationToken Ct { get; init; }

    private bool _answered;

    public ITelegramBotClient Bot => App.Bot;

    public bool Has(string perm) => User.Has(perm);

    // Panel

    public async Task Show(string text, InlineKeyboardMarkup? kb = null)
    {
        text = Fmt.Cut(text, 3900);
        if (MsgId > 0 && !MsgIsMedia)
        {
            try
            {
                await Bot.EditMessageText(ChatId, MsgId, text, ParseMode.Html, kb, new LinkPreviewOptions { IsDisabled = true }, cancellationToken: Ct);
                return;
            }
            catch (ApiRequestException ex) when (ex.Message.Contains("not modified"))
            {
                return;
            }
            catch (ApiRequestException ex)
            {
                Log.Warn("edit failed, sending a new panel: " + ex.Message);
            }
        }
        var old = MsgId > 0 ? MsgId : PanelId;
        var sent = await Bot.SendMessage(ChatId, text, ParseMode.Html, replyMarkup: kb, linkPreviewOptions: new LinkPreviewOptions { IsDisabled = true }, cancellationToken: Ct);
        await Adopt(sent.Id, old);
    }

    public async Task ShowPhoto(byte[] bytes, string fileName, string caption, InlineKeyboardMarkup? kb, bool asDocument)
    {
        caption = Fmt.Cut(caption, 1000);
        if (MsgId > 0 && MsgIsMedia)
        {
            try
            {
                var file = InputFile.FromStream(new MemoryStream(bytes), fileName);
                InputMedia media = asDocument
                    ? new InputMediaDocument(file) { Caption = caption, ParseMode = ParseMode.Html }
                    : new InputMediaPhoto(file) { Caption = caption, ParseMode = ParseMode.Html };
                await Bot.EditMessageMedia(ChatId, MsgId, media, kb, cancellationToken: Ct);
                return;
            }
            catch (ApiRequestException ex)
            {
                Log.Warn("edit media failed, sending a new panel: " + ex.Message);
            }
        }
        var old = MsgId > 0 ? MsgId : PanelId;
        Message sent;
        await using (var ms = new MemoryStream(bytes))
        {
            if (asDocument)
            {
                sent = await Bot.SendDocument(ChatId, InputFile.FromStream(ms, fileName), caption, ParseMode.Html, replyMarkup: kb, cancellationToken: Ct);
            }
            else
            {
                try
                {
                    sent = await Bot.SendPhoto(ChatId, InputFile.FromStream(ms, fileName), caption, ParseMode.Html, replyMarkup: kb, cancellationToken: Ct);
                }
                catch (ApiRequestException ex)
                {
                    Log.Warn("photo rejected, sending as document: " + ex.Message);
                    ms.Position = 0;
                    sent = await Bot.SendDocument(ChatId, InputFile.FromStream(ms, fileName), caption, ParseMode.Html, replyMarkup: kb, cancellationToken: Ct);
                }
            }
        }
        MsgIsMedia = true;
        await Adopt(sent.Id, old, true);
    }

    private async Task Adopt(int newId, int oldId, bool media = false)
    {
        MsgId = newId;
        MsgIsMedia = media;
        await App.Router.ReportPanel(ChatId, newId);
        if (oldId > 0 && oldId != newId)
        {
            try
            {
                await Bot.DeleteMessage(ChatId, oldId, Ct);
            }
            catch (Exception ex)
            {
                Log.Warn($"delete old panel {oldId}: {ex.Message}");
            }
        }
    }

    public Task<Message> Send(string text, InlineKeyboardMarkup? kb = null) =>
        Bot.SendMessage(ChatId, Fmt.Cut(text, 3900), ParseMode.Html, replyMarkup: kb, linkPreviewOptions: new LinkPreviewOptions { IsDisabled = true }, cancellationToken: Ct);

    public async Task Toast(string text = "", bool alert = false)
    {
        if (CallbackId == null || _answered)
            return;
        _answered = true;
        try
        {
            await Bot.AnswerCallbackQuery(CallbackId, string.IsNullOrEmpty(text) ? null : text, alert, cancellationToken: Ct);
        }
        catch (Exception ex)
        {
            Log.Warn("answer callback: " + ex.Message);
        }
    }

    public async Task<bool> Require(string perm)
    {
        if (Has(perm))
            return true;
        if (CallbackId != null)
            await Toast("Нет прав на это действие", true);
        else
            await Show("🚫 Нет прав на это действие.", new InlineKeyboardMarkup(new[] { new[] { InlineKeyboardButton.WithCallbackData("⬅️ Меню", "menu") } }));
        return false;
    }

    public Task Audit(string action) => App.Router.Audit(User.Id, action);
}
