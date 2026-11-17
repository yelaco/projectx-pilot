// configure-dashboard only updates the stopped laptop's local dashboard scope.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"syscall"

	"github.com/yelaco/projectx-pilot/internal/config"
)

func configure(path, state string, input io.Reader) error {
	return configurePlan(path, state, input, false)
}
func configurePlan(path, state string, input io.Reader, check bool) error {
	raw, err := io.ReadAll(io.LimitReader(input, 16385))
	if err != nil || len(raw) > 16384 {
		return errors.New("invalid remote dashboard configuration")
	}
	var remote config.RemoteDashboard
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(&remote); err != nil {
		return errors.New("invalid remote dashboard configuration")
	}
	var extra any
	if decoder.Decode(&extra) != io.EOF {
		return errors.New("remote dashboard scope must contain one JSON object")
	}
	if err = remote.Validate(); err != nil {
		return err
	}
	if remote.Origin == "" {
		return errors.New("remote dashboard scope is required")
	}
	if check {
		return nil
	}
	lock, err := os.OpenFile(filepath.Join(state, "execution.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		return errors.New("stop the bot before changing remote dashboard scope")
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	c.RemoteDashboard = remote
	return config.Save(path, c)
}
func main() {
	path := flag.String("config", "", "local YAML configuration")
	state := flag.String("state", "", "local execution state directory")
	check := flag.Bool("check", false, "validate scope without changing configuration")
	flag.Parse()
	if *path == "" || *state == "" {
		fmt.Fprintln(os.Stderr, "provide config and state paths")
		os.Exit(1)
	}
	if err := configurePlan(*path, *state, os.Stdin, *check); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	if !*check {
		fmt.Println("Remote dashboard scope saved. Restart remains disarmed.")
	}
}
