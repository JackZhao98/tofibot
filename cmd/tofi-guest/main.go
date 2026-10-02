package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"

	"github.com/JackZhao98/tofibot/internal/computer/guest"
)

func main() {
	root := flag.String("workspace", guest.DefaultRoot, "persistent guest workspace root")
	port := flag.Uint("port", guest.VsockPort, "AF_VSOCK port")
	maxDesktop := flag.Int("max-desktops", guest.DefaultDesktop, "maximum simultaneous Bot desktops")
	idleTimeout := flag.Duration("desktop-idle-timeout", 15*time.Minute, "automatically stop an idle Bot desktop (0 disables cleanup)")
	flag.Parse()
	if os.Geteuid() != 1000 || os.Getegid() != 1000 {
		log.Fatalf("tofi-guest must run as the provisioned non-root uid/gid 1000 (got %d/%d)", os.Geteuid(), os.Getegid())
	}
	svc, err := guest.NewWithIdleTimeout(*root, *maxDesktop, *idleTimeout)
	if err != nil {
		log.Fatal(err)
	}
	svc.SetShutdownHook(func(context.Context) error {
		// The response is written before this hook starts. PID 1 owns poweroff;
		// this unprivileged process only exits after flushing its HTTP socket.
		time.Sleep(250 * time.Millisecond)
		_ = exec.Command("sync").Run()
		os.Exit(0)
		return nil
	})
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := svc.ListenAndServe(ctx, uint32(*port)); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
}
