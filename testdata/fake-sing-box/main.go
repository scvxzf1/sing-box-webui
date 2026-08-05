package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintln(os.Stderr, "missing command")
		os.Exit(2)
	}

	switch os.Args[1] {
	case "version":
		fmt.Println("sing-box version 1.99.0-test")
	case "check":
		checkConfig()
	case "run":
		run()
	case "child":
		select {}
	default:
		fmt.Fprintln(os.Stderr, "unsupported command")
		os.Exit(2)
	}
}

func checkConfig() {
	if len(os.Args) != 4 || os.Args[2] != "-c" {
		fmt.Fprintln(os.Stderr, "usage: check -c <path>")
		os.Exit(2)
	}
	content, err := os.ReadFile(os.Args[3])
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var config map[string]any
	if err := json.Unmarshal(content, &config); err != nil {
		fmt.Fprintln(os.Stderr, "invalid JSON")
		os.Exit(1)
	}
	if invalid, _ := config["invalid"].(bool); invalid {
		fmt.Fprintln(os.Stderr, "configuration rejected")
		os.Exit(1)
	}
}

func run() {
	mode := os.Getenv("FAKE_SING_BOX_MODE")
	switch mode {
	case "crash":
		os.Exit(1)
	case "delay":
		time.Sleep(2 * time.Second)
	case "noisy":
		for index := 0; index < 10000; index++ {
			fmt.Println("fake output line", index)
		}
	case "spawn-child":
		child := exec.Command(os.Args[0], "child")
		if err := child.Start(); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
		fmt.Println("child pid", child.Process.Pid)
	}

	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	<-signals
	if mode == "ignore-term" {
		select {}
	}
}
