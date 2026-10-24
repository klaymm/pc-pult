package main

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os/exec"
	"time"
)

func magicPacket(mac net.HardwareAddr) []byte {
	p := bytes.Repeat([]byte{0xff}, 6)
	for range 16 {
		p = append(p, mac...)
	}
	return p
}

func sendMagic(addr string, mac net.HardwareAddr) error {
	conn, err := net.Dial("udp4", addr)
	if err != nil {
		return err
	}
	defer conn.Close()
	_, err = conn.Write(magicPacket(mac))
	return err
}

func resendWake(ctx context.Context, cfg *Config, online func() bool) {
	for _, d := range []time.Duration{2 * time.Second, 5 * time.Second} {
		sleepCtx(ctx, d)
		if ctx.Err() != nil || online() {
			return
		}
		_ = sendMagic(cfg.Broadcast, cfg.mac)
	}
}

func wake(ctx context.Context, cfg *Config) (sent int, err error) {
	var errs []error
	if err := sendMagic(cfg.Broadcast, cfg.mac); err != nil {
		errs = append(errs, err)
	} else {
		sent++
	}
	if len(cfg.WakeCmd) > 0 {
		cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(cctx, cfg.WakeCmd[0], cfg.WakeCmd[1:]...).CombinedOutput(); err != nil {
			errs = append(errs, errors.New(err.Error()+": "+string(bytes.TrimSpace(out))))
		} else {
			sent++
		}
	}
	return sent, errors.Join(errs...)
}
