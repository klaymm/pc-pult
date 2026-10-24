package main

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Aliases

var zoneAliases = map[string]string{
	"utc": "UTC", "мск": "Europe/Moscow", "msk": "Europe/Moscow",
	"москва": "Europe/Moscow", "санкт-петербург": "Europe/Moscow", "питер": "Europe/Moscow", "спб": "Europe/Moscow",
	"калининград": "Europe/Kaliningrad", "симферополь": "Europe/Simferopol", "севастополь": "Europe/Simferopol", "крым": "Europe/Simferopol",
	"киров": "Europe/Kirov", "волгоград": "Europe/Volgograd", "астрахань": "Europe/Astrakhan", "самара": "Europe/Samara",
	"ульяновск": "Europe/Ulyanovsk", "саратов": "Europe/Saratov", "екатеринбург": "Asia/Yekaterinburg", "омск": "Asia/Omsk",
	"новосибирск": "Asia/Novosibirsk", "барнаул": "Asia/Barnaul", "томск": "Asia/Tomsk", "новокузнецк": "Asia/Novokuznetsk",
	"красноярск": "Asia/Krasnoyarsk", "иркутск": "Asia/Irkutsk", "чита": "Asia/Chita", "якутск": "Asia/Yakutsk",
	"владивосток": "Asia/Vladivostok", "хабаровск": "Asia/Vladivostok", "южно-сахалинск": "Asia/Sakhalin", "магадан": "Asia/Magadan",
	"камчатка": "Asia/Kamchatka", "петропавловск-камчатский": "Asia/Kamchatka", "анадырь": "Asia/Anadyr",
	"минск": "Europe/Minsk", "киев": "Europe/Kyiv", "алматы": "Asia/Almaty", "ташкент": "Asia/Tashkent",
	"тбилиси": "Asia/Tbilisi", "ереван": "Asia/Yerevan", "баку": "Asia/Baku",
}

var zoneRegions = []string{"Europe", "Asia", "America", "Africa", "Australia", "Pacific", "Atlantic", "Indian", "Antarctica", "Arctic"}

var offsetRe = regexp.MustCompile(`^(utc|gmt|мск|msk)?\s*([+-])\s*(\d{1,2})(?::00)?$`)

// Resolve

func hasZone(name string) bool {
	_, err := time.LoadLocation(name)
	return err == nil
}

func titleZone(s string) string {
	var b strings.Builder
	up := true
	for _, r := range strings.ToLower(strings.TrimSpace(s)) {
		switch r {
		case ' ', '_':
			b.WriteByte('_')
			up = true
		case '/', '-':
			b.WriteRune(r)
			up = true
		default:
			if up {
				b.WriteString(strings.ToUpper(string(r)))
			} else {
				b.WriteRune(r)
			}
			up = false
		}
	}
	return b.String()
}

func offsetZone(low string) (string, bool) {
	m := offsetRe.FindStringSubmatch(low)
	if m == nil {
		return "", false
	}
	n, _ := strconv.Atoi(m[3])
	if m[2] == "-" {
		n = -n
	}
	if m[1] == "мск" || m[1] == "msk" {
		n += 3
	}
	if n < -12 || n > 14 {
		return "", false
	}
	if n == 0 {
		return "UTC", true
	}
	sign := "-"
	if n < 0 {
		sign, n = "+", -n
	}
	name := fmt.Sprintf("Etc/GMT%s%d", sign, n)
	return name, hasZone(name)
}

func resolveZone(input string) (string, bool) {
	in := strings.TrimSpace(input)
	low := strings.ToLower(in)
	if in == "" || low == "local" {
		return "", false
	}
	if z, ok := zoneAliases[low]; ok {
		return z, true
	}
	if z, ok := offsetZone(low); ok {
		return z, true
	}
	if hasZone(in) {
		return in, true
	}
	name := titleZone(in)
	if hasZone(name) {
		return name, true
	}
	for _, region := range zoneRegions {
		if z := region + "/" + name; hasZone(z) {
			return z, true
		}
	}
	return "", false
}

// Prompt

func (s *Setup) askZone() (string, error) {
	s.say("")
	s.say("Часовой пояс нужен для времени в сообщениях бота. Введите город (Саратов, Москва), смещение (UTC+4, МСК+1) или название вида Europe/Saratov. Enter - Москва.")
	for {
		raw, err := s.ask("Часовой пояс", "Europe/Moscow")
		if err != nil {
			return "", err
		}
		if zone, ok := resolveZone(raw); ok {
			if zone != raw {
				s.say("Часовой пояс: %s", zone)
			}
			return zone, nil
		}
		s.say("Не нашёл часовой пояс %q. Попробуйте город, например Саратов, смещение UTC+4 или Europe/Saratov.", raw)
	}
}
