using System.Globalization;
using System.Speech.Synthesis;
using System.Text.Json.Nodes;

namespace PcPult.Agent;

public static class Speech
{
    public static async Task<JsonObject> Say(JsonObject a)
    {
        var text = a["text"]?.GetValue<string>() ?? "";
        if (string.IsNullOrWhiteSpace(text))
            throw new InvalidOperationException("Нет текста");
        return await Task.Run(() =>
        {
            using var synth = new SpeechSynthesizer();
            var voices = synth.GetInstalledVoices().Where(v => v.Enabled).ToList();
            if (voices.Count == 0)
                throw new InvalidOperationException("В Windows не установлено ни одного голоса");
            var russian = voices.FirstOrDefault(v => v.VoiceInfo.Culture.TwoLetterISOLanguageName == "ru");
            var wantsRussian = text.Any(ch => ch is >= 'А' and <= 'я' or 'Ё' or 'ё');
            if (russian != null)
                synth.SelectVoice(russian.VoiceInfo.Name);
            synth.Speak(text);
            return new JsonObject
            {
                ["voice"] = russian?.VoiceInfo.Name ?? voices[0].VoiceInfo.Name,
                ["russian"] = russian != null,
                ["warn"] = wantsRussian && russian == null,
            };
        });
    }
}
