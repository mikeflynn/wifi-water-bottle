package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/controlplane"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/model"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/provisioncontrol"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/tunnel"
	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/wigle"
)

func main() {
	if len(os.Args) == 1 {
		if err := runTUI(); err != nil {
			fmt.Fprintln(os.Stderr, "error:", err)
			os.Exit(1)
		}
		return
	}
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: bottle-tui <control|wigle> ...")
	}
	if args[0] == "control" {
		return runControl(args[1:], out, errOut)
	}
	if args[0] != "wigle" {
		return errors.New("usage: bottle-tui <control|wigle> ...")
	}
	if len(args) < 2 {
		return errors.New("usage: bottle-tui wigle <preview|export|upload|credentials> ...")
	}
	switch args[1] {
	case "preview":
		input, err := parseInput(args[2:])
		if err != nil {
			return err
		}
		_, preview, err := loadPreview(input)
		if err != nil {
			return err
		}
		printPreview(out, preview)
		return nil
	case "export":
		fs := flag.NewFlagSet("export", flag.ContinueOnError)
		fs.SetOutput(errOut)
		input := fs.String("input", "", "capture JSON file")
		output := fs.String("output", "", "WiGLE CSV destination")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *input == "" || *output == "" {
			return errors.New("export requires --input and --output")
		}
		_, preview, err := loadPreview(*input)
		if err != nil {
			return err
		}
		printPreview(out, preview)
		if preview.Valid == 0 {
			return errors.New("no valid records to export")
		}
		file, err := os.Create(*output)
		if err != nil {
			return err
		}
		defer file.Close()
		if err = wigle.WriteCSV(file, preview.Records, wigle.DeviceMetadata{AppRelease: "0.1.0", Model: "wifi-water-bottle"}); err != nil {
			return err
		}
		fmt.Fprintf(out, "Exported %d valid record(s) to %s\n", preview.Valid, *output)
		return nil
	case "upload":
		fs := flag.NewFlagSet("upload", flag.ContinueOnError)
		fs.SetOutput(errOut)
		input := fs.String("input", "", "capture JSON file")
		confirm := fs.Bool("confirm", false, "explicitly authorize external WiGLE upload")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *input == "" {
			return errors.New("upload requires --input")
		}
		_, preview, err := loadPreview(*input)
		if err != nil {
			return err
		}
		printPreview(out, preview)
		if !*confirm {
			return wigle.ErrConfirmationRequired
		}
		if preview.Valid == 0 {
			return errors.New("no valid records to upload")
		}
		var csv strings.Builder
		if err := wigle.WriteCSV(&csv, preview.Records, wigle.DeviceMetadata{AppRelease: "0.1.0", Model: "wifi-water-bottle"}); err != nil {
			return err
		}
		result, err := (wigle.Uploader{Credentials: wigle.KeyringStore{}}).Upload(context.Background(), strings.TrimSuffix(filepath.Base(*input), filepath.Ext(*input))+".wiglecsv", []byte(csv.String()), true)
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "WiGLE upload accepted: transaction=%s attempts=%d\n", result.TransactionID, result.Attempts)
		return nil
	case "credentials":
		if len(args) != 3 || args[2] != "set" {
			return errors.New("usage: bottle-tui wigle credentials set (reads API name and token from standard input)")
		}
		var apiName, token string
		fmt.Fprint(out, "WiGLE API name: ")
		if _, err := fmt.Fscan(in, &apiName); err != nil {
			return err
		}
		fmt.Fprint(out, "WiGLE API token: ")
		if _, err := fmt.Fscan(in, &token); err != nil {
			return err
		}
		if err := (wigle.KeyringStore{}).Save(context.Background(), wigle.Credentials{APIName: apiName, Token: token}); err != nil {
			return err
		}
		fmt.Fprintln(out, "WiGLE credentials stored in the operating-system credential store.")
		return nil
	default:
		return fmt.Errorf("unknown wigle command %q", args[1])
	}
}

func runControl(args []string, out, errOut io.Writer) error {
	if len(args) == 0 {
		return errors.New("usage: bottle-tui control <profile|status|provision|update|survey|tunnel>")
	}
	if args[0] == "profile" {
		if len(args) < 2 || args[1] != "import" {
			return errors.New("usage: bottle-tui control profile import --ca FILE --cert FILE --key FILE --id ID")
		}
		fs := flag.NewFlagSet("profile import", flag.ContinueOnError)
		fs.SetOutput(errOut)
		caPath := fs.String("ca", "", "pinned Pi CA PEM file")
		certPath := fs.String("cert", "", "client certificate PEM file")
		keyPath := fs.String("key", "", "client private key PEM file")
		id := fs.String("id", "", "profile ID")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if *caPath == "" || *certPath == "" || *keyPath == "" || *id == "" {
			return errors.New("profile import requires --ca, --cert, --key, and --id")
		}
		read := func(path string) ([]byte, error) {
			b, err := os.ReadFile(path)
			if err != nil {
				return nil, fmt.Errorf("read profile material: %w", err)
			}
			return b, nil
		}
		ca, err := read(*caPath)
		if err != nil {
			return err
		}
		cert, err := read(*certPath)
		if err != nil {
			return err
		}
		key, err := read(*keyPath)
		if err != nil {
			return err
		}
		if err := (controlplane.KeyringStore{}).Save(context.Background(), controlplane.Credentials{CAPEM: ca, CertificatePEM: cert, PrivateKeyPEM: key, ClientID: *id}); err != nil {
			return err
		}
		fmt.Fprintln(out, "Control-plane profile stored in the operating-system credential store.")
		return nil
	}
	credentials, err := (controlplane.KeyringStore{}).Load(context.Background())
	if err != nil {
		return err
	}
	client, err := controlplane.NewClient(controlplane.PiAddress, credentials)
	if err != nil {
		return err
	}
	switch args[0] {
	case "status":
		status, err := client.Status(context.Background())
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Pi ready=%t survey=%s message=%s\n", status.Ready, status.Survey, status.Message)
		return nil
	case "provision":
		fs := flag.NewFlagSet("provision", flag.ContinueOnError)
		fs.SetOutput(errOut)
		id := fs.String("request-id", "", "stable request ID")
		confirm := fs.Bool("confirm", false, "confirm configuration changes")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" {
			return errors.New("provision requires --request-id")
		}
		job, err := (provisioncontrol.New(client)).Provision(context.Background(), provisioncontrol.ProvisionRequest{RequestID: *id, Confirmed: *confirm})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Provision %s: %s (%s)\n", job.ID, job.State, job.Message)
		return nil
	case "update":
		fs := flag.NewFlagSet("update", flag.ContinueOnError)
		fs.SetOutput(errOut)
		id := fs.String("request-id", "", "stable request ID")
		version := fs.String("version", "", "release version")
		channel := fs.String("channel", "stable", "release channel")
		confirm := fs.Bool("confirm", false, "confirm update")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		if *id == "" || *version == "" {
			return errors.New("update requires --request-id and --version")
		}
		job, err := (provisioncontrol.New(client)).Update(context.Background(), provisioncontrol.UpdateRequest{RequestID: *id, Version: *version, Channel: *channel, Confirmed: *confirm})
		if err != nil {
			return err
		}
		fmt.Fprintf(out, "Update %s: %s (%s)\n", job.ID, job.State, job.Message)
		return nil
	case "survey":
		if len(args) < 2 || (args[1] != "start" && args[1] != "stop") {
			return errors.New("usage: bottle-tui control survey <start|stop> --confirm")
		}
		fs := flag.NewFlagSet("survey", flag.ContinueOnError)
		fs.SetOutput(errOut)
		confirm := fs.Bool("confirm", false, "confirm survey control")
		if err := fs.Parse(args[2:]); err != nil {
			return err
		}
		if err := client.Survey(context.Background(), args[1] == "start", *confirm); err != nil {
			return err
		}
		fmt.Fprintf(out, "Survey %s requested.\n", args[1])
		return nil
	case "logs":
		events, errs := client.StreamEvents(context.Background(), 0)
		return renderLiveLogs(context.Background(), out, events, errs)
	case "tunnel":
		fs := flag.NewFlagSet("tunnel", flag.ContinueOnError)
		fs.SetOutput(errOut)
		port := fs.Int("port", 2501, "local loopback port")
		if err := fs.Parse(args[1:]); err != nil {
			return err
		}
		opener, err := controlplane.NewMTLSOpener(client)
		if err != nil {
			return err
		}
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		t, err := tunnel.Start(ctx, *port, opener)
		if err != nil {
			return err
		}
		defer t.Close()
		fmt.Fprintf(out, "Kismet tunnel listening at %s\n", t.URL())
		for {
			select {
			case event, ok := <-t.Events():
				if !ok {
					return nil
				}
				if event.Err != nil {
					fmt.Fprintf(errOut, "tunnel %s: %v\n", event.Status, event.Err)
				} else {
					fmt.Fprintf(out, "tunnel %s: %s\n", event.Status, event.Message)
				}
			case <-time.After(250 * time.Millisecond):
			}
		}
	default:
		return fmt.Errorf("unknown control command %q", args[0])
	}
}

// renderLiveLogs writes a redacted NDJSON event view and preserves explicit
// history-gap markers instead of silently treating them as reconnects.
func renderLiveLogs(ctx context.Context, out io.Writer, events <-chan controlplane.Event, errs <-chan error) error {
	buffer := model.NewBuffer(model.BufferConfig{MaxEvents: 10_000})
	encoder := json.NewEncoder(out)
	for events != nil || errs != nil {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case event, ok := <-events:
			if !ok {
				events = nil
				continue
			}
			if err := buffer.Add(event.Event); err != nil {
				continue
			}
			visible := buffer.Visible()
			if err := encoder.Encode(visible[len(visible)-1]); err != nil {
				return fmt.Errorf("write live log: %w", err)
			}
		case err, ok := <-errs:
			if !ok {
				errs = nil
				continue
			}
			if errors.Is(err, controlplane.ErrResyncRequired) {
				buffer.MarkHistoryGap(err.Error())
				visible := buffer.Visible()
				if err := encoder.Encode(visible[len(visible)-1]); err != nil {
					return fmt.Errorf("write live log history gap: %w", err)
				}
				continue
			}
			if err != nil && !errors.Is(err, context.Canceled) {
				return fmt.Errorf("live log stream: %w", err)
			}
		}
	}
	return nil
}

func parseInput(args []string) (string, error) {
	fs := flag.NewFlagSet("preview", flag.ContinueOnError)
	input := fs.String("input", "", "capture JSON file")
	if err := fs.Parse(args); err != nil {
		return "", err
	}
	if *input == "" {
		return "", errors.New("preview requires --input")
	}
	return *input, nil
}
func loadPreview(path string) ([]wigle.Record, wigle.PreviewResult, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, wigle.PreviewResult{}, err
	}
	defer file.Close()
	records, err := wigle.DecodeRecords(file)
	if err != nil {
		return nil, wigle.PreviewResult{}, err
	}
	return records, wigle.Preview(records), nil
}
func printPreview(out io.Writer, preview wigle.PreviewResult) {
	fmt.Fprintf(out, "Preview: %d valid, %d invalid record(s)\n", preview.Valid, preview.Invalid)
	for _, issue := range preview.Issues {
		fmt.Fprintf(out, "  record %d skipped: %s\n", issue.Index, issue.Reason)
	}
}
