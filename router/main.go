package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	_ "time/tzdata"

	_ "golang.org/x/crypto/x509roots/fallback"
)

var version = "dev"

func main() {
	cfgPath := flag.String("config", "/opt/etc/pc-pult/config.json", "config file")
	setToken := flag.Bool("set-token", false, "ask for the bot token and save it to the config")
	showVersion := flag.Bool("version", false, "print version")
	superviseFlag := flag.Bool("supervise", false, "run as a child process and restart it on crash")
	check := flag.Bool("check", false, "validate the config; exit code 2 means the token is empty")
	setup := flag.Bool("setup", false, "first-time setup: bot, owner and PC pairing")
	pair := flag.Bool("pair", false, "pair another PC with this router")
	force := flag.Bool("force", false, "setup: replace an existing config")
	noStart := flag.Bool("no-start", false, "setup and pair: do not touch the running service")
	port := flag.String("port", "8765", "setup: port for the PC link")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	if *superviseFlag {
		os.Exit(supervise(withoutFlag(os.Args[1:], "supervise")))
	}
	if *setup {
		os.Exit(runSetup(*cfgPath, *port, *force, *noStart))
	}

	cfg, err := loadConfig(*cfgPath)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	tr, err := newTransport(cfg.TGProxy)
	if err != nil {
		fmt.Fprintln(os.Stderr, "tg_proxy:", err)
		os.Exit(1)
	}
	if *setToken {
		os.Exit(runSetToken(cfg, tr))
	}
	if *pair {
		os.Exit(runPair(cfg, *noStart))
	}
	if *check {
		if cfg.Token == "" {
			fmt.Println("config ok, token empty")
			os.Exit(2)
		}
		fmt.Println("config ok")
		return
	}
	if cfg.Token == "" {
		fmt.Fprintln(os.Stderr, "token is empty, run with -set-token")
		os.Exit(1)
	}
	if cfg.LogFile != "" {
		if w, err := newRotator(cfg.LogFile, 1<<20); err == nil {
			log.SetOutput(w)
			redirectStderr(cfg.LogFile + ".crash")
		} else {
			fmt.Fprintln(os.Stderr, "log:", err)
		}
	}

	store, err := openStore(filepath.Join(cfg.DataDir, "users.json"), cfg.OwnerID)
	if err != nil {
		log.Fatalf("users: %v", err)
	}
	audit := openAudit(filepath.Join(cfg.DataDir, "audit.jsonl"))
	link := newPCLink()
	tg := newTG(cfg.TGAPI, cfg.Token, tr)
	panels := openPanels(filepath.Join(cfg.DataDir, "panels.json"))
	srv, err := newServer(cfg, link, store, audit, tr)
	if err != nil {
		log.Fatalf("server: %v", err)
	}
	srv.panels = panels
	bot := newBot(cfg, tg, store, link, audit)
	bot.panels = panels

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	hs := &http.Server{Handler: srv.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 120 * time.Second}
	go serve(ctx, hs, cfg.Listen)
	go link.Watch(ctx)

	log.Printf("pc-pult-router %s started", version)
	bot.Run(ctx)

	sctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	_ = hs.Shutdown(sctx)
	log.Printf("stopped")
}

func serve(ctx context.Context, hs *http.Server, addr string) {
	for ctx.Err() == nil {
		ln, err := net.Listen("tcp", addr)
		if err != nil {
			log.Printf("listen %s: %v", addr, err)
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		log.Printf("listening on %s", addr)
		if err := hs.Serve(ln); err != nil && err != http.ErrServerClosed {
			log.Printf("serve: %v", err)
			sleepCtx(ctx, 5*time.Second)
			continue
		}
		return
	}
}

// Supervisor

func withoutFlag(args []string, name string) []string {
	var out []string
	for _, a := range args {
		if a == "-"+name || a == "--"+name {
			continue
		}
		out = append(out, a)
	}
	return out
}

func supervise(args []string) int {
	exe, err := os.Executable()
	if err != nil {
		return 1
	}
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, os.Interrupt, syscall.SIGTERM)
	backoff := time.Second
	for {
		cmd := exec.Command(exe, args...)
		cmd.Stdout, cmd.Stderr = os.Stdout, os.Stderr
		start := time.Now()
		if err := cmd.Start(); err != nil {
			return 1
		}
		done := make(chan error, 1)
		go func() { done <- cmd.Wait() }()
		select {
		case s := <-sig:
			_ = cmd.Process.Signal(s)
			select {
			case <-done:
			case <-time.After(5 * time.Second):
				_ = cmd.Process.Kill()
			}
			return 0
		case err := <-done:
			if err == nil {
				return 0
			}
		}
		if time.Since(start) > time.Minute {
			backoff = time.Second
		}
		select {
		case <-sig:
			return 0
		case <-time.After(backoff):
		}
		backoff = min(backoff*2, time.Minute)
	}
}

// Setup

func runSetup(cfgPath, port string, force, noStart bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newSetup(cfgPath, port).Run(ctx, force, noStart); err != nil {
		fmt.Println("Настройка не завершена:", err)
		return 1
	}
	return 0
}

func runPair(cfg *Config, noStart bool) int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newSetup(cfg.path, "").Pair(ctx, cfg, noStart); err != nil {
		fmt.Println("Подключение не удалось:", err)
		return 1
	}
	return 0
}

func runSetToken(cfg *Config, tr http.RoundTripper) int {
	fmt.Print("Токен бота от @BotFather (ввод не отображается): ")
	token := strings.TrimSpace(readSecret(bufio.NewReader(os.Stdin)))
	fmt.Println()
	if token == "" {
		fmt.Println("Токен пустой, ничего не изменено.")
		return 1
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var me TUser
	if err := newTG(cfg.TGAPI, token, tr).Call(ctx, "getMe", nil, &me); err != nil {
		fmt.Println("Токен не подошёл:", strings.ReplaceAll(err.Error(), token, "<token>"))
		return 1
	}
	if err := cfg.saveToken(token); err != nil {
		fmt.Println("Не получилось сохранить:", err)
		return 1
	}
	fmt.Printf("Готово: бот @%s, токен сохранён.\n", me.Username)
	return 0
}

func sttyRun(arg string) {
	c := exec.Command("stty", arg)
	c.Stdin = os.Stdin
	_ = c.Run()
}

func readSecret(r *bufio.Reader) string {
	sttyRun("-echo")
	defer sttyRun("echo")
	line, _ := r.ReadString('\n')
	return line
}
