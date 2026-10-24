using Telegram.Bot.Types.ReplyMarkups;

namespace PcPult.Service;

public static class Ui
{
    public static InlineKeyboardButton B(string text, string data) => InlineKeyboardButton.WithCallbackData(text, data);

    public static InlineKeyboardMarkup Kb(params InlineKeyboardButton[][] rows) =>
        new(rows.Where(r => r.Length > 0));

    public static InlineKeyboardButton[] R(params InlineKeyboardButton[] buttons) => buttons;

    public static InlineKeyboardButton[] None => [];

    public static InlineKeyboardButton[] If(bool cond, params InlineKeyboardButton[] buttons) => cond ? buttons : [];

    public static InlineKeyboardButton[] BackMenu => [B("⬅️ Меню", "menu")];
}
