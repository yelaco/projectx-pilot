// configure-tunnel prepares local named-tunnel configuration while the bot is stopped.
// It never calls Cloudflare or a trading API, and never arms execution.
package main

import (
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"syscall"

	"github.com/yelaco/projectx-pilot/internal/config"
	"github.com/yelaco/projectx-pilot/internal/ingress"
)

var hostnamePattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?(?:\.[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?)+$`)
var tokenPattern = regexp.MustCompile(`^[A-Za-z0-9_+/=-]+$`)

func main() {
	hostname := flag.String("hostname", "", "permanent webhook hostname")
	state := flag.String("state", "", "stopped bot's state directory")
	conf := flag.String("config-dir", "", "private configuration directory")
	check := flag.Bool("check", false, "validate input without writing configuration")
	flag.Parse()
	if err := configurePlan(*hostname, *state, *conf, os.Stdin, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !*check {
		fmt.Println("Named tunnel configured locally. Execution remains paused on startup.")
	}
}
func configure(hostname, state, conf string, input io.Reader) error {
	return configurePlan(hostname, state, conf, input, false)
}
func configurePlan(hostname, state, conf string, input io.Reader, check bool) error {
	hostname = strings.ToLower(strings.TrimSpace(hostname))
	if len(hostname) > 253 || !hostnamePattern.MatchString(hostname) || state == "" || conf == "" {
		return errors.New("provide a valid hostname and local state/configuration directories")
	}
	// Bound secret input and use fixed errors: never include the token in output.
	b, err := io.ReadAll(io.LimitReader(input, 16385))
	if err != nil || len(b) > 16384 {
		return errors.New("could not read bounded tunnel token")
	}
	token := strings.TrimSpace(string(b))
	if len(token) < 32 || !tokenPattern.MatchString(token) {
		return errors.New("provide only the tunnel token, not the installation command")
	}
	if check {
		return nil
	}
	if err = os.MkdirAll(conf, 0700); err != nil {
		return errors.New("could not create private configuration directory")
	}
	if err = os.MkdirAll(state, 0700); err != nil {
		return errors.New("could not create state directory")
	}
	lock, err := os.OpenFile(filepath.Join(state, "execution.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return errors.New("could not open execution lease")
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("stop the bot before configuring its named tunnel")
	}
	// Validate existing routing before replacing anything. Preserve delivery tokens/mappings.
	path := filepath.Join(state, "ingress.json")
	s, err := ingress.Load(path)
	if os.IsNotExist(err) {
		s = ingress.Settings{}
	} else if err != nil {
		return errors.New("existing webhook configuration is invalid; no configuration was changed")
	}
	s.Enabled = true
	s.QuickTunnel = false
	s.PublicURL = "https://" + hostname
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return errors.New("could not encode webhook configuration")
	}
	if err = config.WritePrivate(filepath.Join(conf, "cloudflared-token"), []byte(token+"\n")); err != nil {
		return errors.New("could not save private tunnel token")
	}
	if err = config.WritePrivate(path, data); err != nil {
		return errors.New("could not save webhook routing; keep services stopped and rerun setup")
	}
	return nil
}
