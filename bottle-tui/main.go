package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/mikeflynn/wifi-water-bottle/bottle-tui/internal/wigle"
)

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func run(args []string, in io.Reader, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] != "wigle" {
		return errors.New("usage: bottle-tui wigle <preview|export|upload|credentials> ...")
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
