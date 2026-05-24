package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strconv"
	"strings"
	"time"

	lpaccid "github.com/damonto/euicc-go/driver/ccid"
	"github.com/damonto/euicc-go/lpa"
	sourceccid "github.com/damonto/ts43-go/driver/ccid"
	"github.com/damonto/ts43-go/ts43"
)

type cliOptions struct {
	Server                    string
	SourceReader              string
	TargetReader              string
	ListReaders               bool
	Debug                     bool
	UsePost                   bool
	SourcePSIM                bool
	UseTempTokenConversion    bool
	SetTargetTerminalFields   bool
	RestrictOldTerminalFields bool
	UseSMDSDelayedDownload    bool
	Version                   string
	AppName                   string
	TransferType              int
	Timeout                   time.Duration
	PollInterval              time.Duration
	PollTimeout               time.Duration
	AutoRetry511Limit         int
	SourceDevice              ts43.Device
	TargetDevice              ts43.Device
}

var errCCIDUnavailable = errors.New("CCID unavailable")

func main() {
	if err := run(os.Args[1:], os.Stdin, os.Stdout, os.Stderr); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return
		}
		fmt.Fprintf(os.Stderr, "esim-transfer: %v\n", err)
		os.Exit(1)
	}
}

func run(args []string, stdin io.Reader, stdout, stderr io.Writer) (err error) {
	opts, err := parseFlags(args, stderr)
	if err != nil {
		return err
	}

	logger := slog.New(slog.NewTextHandler(stderr, &slog.HandlerOptions{
		Level: logLevel(opts.Debug),
	}))
	slog.SetDefault(logger)

	if opts.ListReaders {
		return printReaders(stdout)
	}
	if err := opts.validate(); err != nil {
		return err
	}

	reader := bufio.NewReader(stdin)
	source, err := sourceccid.New()
	if err != nil {
		return openCCIDError("source", err)
	}
	defer func() {
		if closeErr := source.Disconnect(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close source reader: %w", closeErr))
		}
	}()

	sourceReaders, err := source.ListReaders()
	if err != nil {
		return fmt.Errorf("list source readers: %w", err)
	}
	sourceReader, err := chooseReader(reader, stdout, sourceReaders, opts.SourceReader, "source")
	if err != nil {
		return fmt.Errorf("choose source reader: %w", err)
	}
	if err := source.SetReader(sourceReader); err != nil {
		return fmt.Errorf("select source reader: %w", err)
	}

	target, err := lpaccid.New()
	if err != nil {
		return openCCIDError("target", err)
	}
	defer func() {
		if closeErr := disconnectCCID(target); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close target reader: %w", closeErr))
		}
	}()

	targetReaders, err := target.ListReaders()
	if err != nil {
		return fmt.Errorf("list target readers: %w", err)
	}
	targetReader, err := chooseReaderWithDefault(reader, stdout, targetReaders, opts.TargetReader, "target", defaultReaderIndex(targetReaders, sourceReader), sourceReader)
	if err != nil {
		return fmt.Errorf("choose target reader: %w", err)
	}
	target.SetReader(targetReader)

	lpaClient, err := lpa.New(&lpa.Options{
		Channel: target,
		Logger:  logger,
		Timeout: opts.Timeout,
	})
	if err != nil {
		return fmt.Errorf("create LPA client: %w", err)
	}
	defer func() {
		if closeErr := lpaClient.Close(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close LPA client: %w", closeErr))
		}
	}()

	var sourceLPAClient *lpa.Client
	var sourceLPAChannel lpaccid.CCID
	defer func() {
		if sourceLPAClient != nil {
			if closeErr := sourceLPAClient.Close(); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close source LPA client: %w", closeErr))
			}
		}
		if sourceLPAChannel != nil {
			if closeErr := disconnectCCID(sourceLPAChannel); closeErr != nil {
				err = errors.Join(err, fmt.Errorf("close source LPA reader: %w", closeErr))
			}
		}
	}()
	sourceLPA := func() (*lpa.Client, error) {
		if sourceLPAClient != nil {
			return sourceLPAClient, nil
		}
		if err := source.Disconnect(); err != nil {
			return nil, fmt.Errorf("close source TS.43 reader before source LPA: %w", err)
		}
		channel, err := lpaccid.New()
		if err != nil {
			return nil, err
		}
		channel.SetReader(sourceReader)
		sourceClient, err := lpa.New(&lpa.Options{
			Channel: channel,
			Logger:  logger,
			Timeout: opts.Timeout,
		})
		if err != nil {
			_ = disconnectCCID(channel)
			return nil, err
		}
		sourceLPAChannel = channel
		sourceLPAClient = sourceClient
		return sourceLPAClient, nil
	}

	if opts.TargetDevice.EID == "" {
		eid, err := lpaClient.EID()
		if err != nil {
			return fmt.Errorf("read target EID: %w", err)
		}
		opts.TargetDevice.EID = fmt.Sprintf("%X", eid)
	}
	fmt.Fprintf(stdout, "source_reader=%s\n", sourceReader)
	fmt.Fprintf(stdout, "target_reader=%s\n", targetReader)
	fmt.Fprintf(stdout, "target_eid=%s\n", opts.TargetDevice.EID)

	client, err := ts43.New(&ts43.Config{
		Logger: logger,
		Entitlement: ts43.Entitlement{
			Server:                    opts.Server,
			Version:                   opts.Version,
			UsePost:                   opts.UsePost,
			PSIM:                      opts.SourcePSIM,
			TransferType:              ts43.TransferType(opts.TransferType),
			UseTempTokenConversion:    opts.UseTempTokenConversion,
			SetTargetTerminalFields:   opts.SetTargetTerminalFields,
			RestrictOldTerminalFields: opts.RestrictOldTerminalFields,
			AppName:                   opts.AppName,
			Timeout:                   opts.Timeout,
			PollInterval:              opts.PollInterval,
			PollTimeout:               opts.PollTimeout,
			AutoRetry511Limit:         opts.AutoRetry511Limit,
			UseSMDSDelayedDownload:    opts.UseSMDSDelayedDownload,
		},
		Source: ts43.Endpoint{
			Channel: source,
			Device:  opts.SourceDevice,
		},
		Target: ts43.Endpoint{
			Device: opts.TargetDevice,
		},
	})
	if err != nil {
		return fmt.Errorf("create TS.43 client: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	return runTransfer(ctx, reader, stdout, client, lpaClient, sourceLPA)
}

func parseFlags(args []string, stderr io.Writer) (cliOptions, error) {
	var opts cliOptions
	opts.Version = "9.0"
	opts.AppName = "androidTransfer"
	opts.Timeout = 30 * time.Second
	opts.PollInterval = 30 * time.Second
	opts.PollTimeout = 10 * time.Minute

	fs := flag.NewFlagSet("esim-transfer", flag.ContinueOnError)
	fs.SetOutput(stderr)
	fs.Usage = func() {
		fmt.Fprintf(stderr, "Usage: %s -server URL -source-imei IMEI -target-imei IMEI [flags]\n", fs.Name())
		fmt.Fprintln(stderr, "")
		fmt.Fprintln(stderr, "Pass the carrier-config transfer type for the selected source profile.")
		fmt.Fprintln(stderr, "For Vodafone CZ, GMS uses -transfer-type 2 for pSIM sources and 3 for eSIM sources.")
		fmt.Fprintln(stderr, "The selected source reader is treated as the source profile; no source profile list is built.")
		fmt.Fprintln(stderr, "")
		fs.PrintDefaults()
	}

	fs.StringVar(&opts.Server, "server", "", "TS.43 entitlement server URL")
	fs.StringVar(&opts.SourceDevice.IMEI, "source-imei", "", "source device IMEI")
	fs.StringVar(&opts.TargetDevice.IMEI, "target-imei", "", "target device IMEI")
	fs.StringVar(&opts.SourceReader, "source-reader", "", "source SIM CCID reader name")
	fs.StringVar(&opts.TargetReader, "target-reader", "", "target eUICC CCID reader name")
	fs.BoolVar(&opts.ListReaders, "list-readers", false, "list available CCID readers and exit")
	fs.BoolVar(&opts.Debug, "debug", false, "enable debug logging")

	fs.StringVar(&opts.SourceDevice.EID, "source-eid", "", "source device EID")
	fs.StringVar(&opts.SourceDevice.Vendor, "source-vendor", "", "source device vendor")
	fs.StringVar(&opts.SourceDevice.Model, "source-model", "", "source device model")
	fs.StringVar(&opts.SourceDevice.SoftwareVersion, "source-software", "", "source device software version")
	fs.StringVar(&opts.TargetDevice.EID, "target-eid", "", "target eUICC EID; read from target if empty")
	fs.StringVar(&opts.TargetDevice.Vendor, "target-vendor", "", "target device vendor")
	fs.StringVar(&opts.TargetDevice.Model, "target-model", "", "target device model")
	fs.StringVar(&opts.TargetDevice.SoftwareVersion, "target-software", "", "target device software version")

	fs.StringVar(&opts.Version, "version", opts.Version, "TS.43 entitlement version")
	fs.StringVar(&opts.AppName, "app-name", opts.AppName, "TS.43 app name")
	fs.BoolVar(&opts.UsePost, "use-post", false, "send TS.43 service entitlement requests with POST")
	fs.BoolVar(&opts.SourcePSIM, "source-psim", false, "source subscription is pSIM; otherwise eSIM")
	fs.BoolVar(&opts.UseTempTokenConversion, "use-temp-token-conversion", false, "use temporary token for pSIM-to-eSIM conversion")
	fs.BoolVar(&opts.SetTargetTerminalFields, "set-target-terminal-fields", false, "carrier entitlement setTargetTerminalFields value")
	fs.BoolVar(&opts.RestrictOldTerminalFields, "restrict-old-terminal-fields", false, "GMS Ljotr.Q value; when true only transfer types 7 and 9 send old terminal fields")
	fs.IntVar(&opts.TransferType, "transfer-type", 0, "TS.43 transfer type from carrier config; Vodafone CZ uses 2 for pSIM and 3 for eSIM")
	fs.BoolVar(&opts.UseSMDSDelayedDownload, "use-smds-delayed-download", false, "handle delayed download through SM-DS discovery")
	fs.DurationVar(&opts.Timeout, "timeout", opts.Timeout, "HTTP request timeout")
	fs.DurationVar(&opts.PollInterval, "poll-interval", opts.PollInterval, "default TS.43 polling interval")
	fs.DurationVar(&opts.PollTimeout, "poll-timeout", opts.PollTimeout, "TS.43 polling timeout")
	fs.IntVar(&opts.AutoRetry511Limit, "auto-retry-511", 0, "number of automatic retries after HTTP 511")

	if err := fs.Parse(args); err != nil {
		return cliOptions{}, err
	}
	if fs.NArg() > 0 {
		return cliOptions{}, fmt.Errorf("unexpected positional arguments: %s", strings.Join(fs.Args(), " "))
	}
	return opts, nil
}

func (opts cliOptions) validate() error {
	if opts.Server == "" {
		return errors.New("server is required")
	}
	if opts.SourceDevice.IMEI == "" {
		return errors.New("source IMEI is required")
	}
	if opts.TargetDevice.IMEI == "" {
		return errors.New("target IMEI is required")
	}
	return nil
}

func logLevel(debug bool) slog.Level {
	if debug {
		return slog.LevelDebug
	}
	return slog.LevelInfo
}

func printReaders(w io.Writer) error {
	readers, err := listCCIDReaders()
	if errors.Is(err, errCCIDUnavailable) {
		return nil
	}
	if err != nil {
		return err
	}
	for i := range len(readers) {
		fmt.Fprintln(w, readerDisplayName(i, readers[i]))
	}
	return nil
}

func listCCIDReaders() (_ []string, err error) {
	channel, err := sourceccid.New()
	if err != nil {
		if ccidUnavailable(err) {
			return nil, errCCIDUnavailable
		}
		return nil, fmt.Errorf("open CCID context: %w", err)
	}
	defer func() {
		if closeErr := channel.Disconnect(); closeErr != nil {
			err = errors.Join(err, fmt.Errorf("close reader list context: %w", closeErr))
		}
	}()

	readers, err := channel.ListReaders()
	if err != nil {
		return nil, fmt.Errorf("list readers: %w", err)
	}
	return readers, nil
}

func disconnectCCID(channel any) error {
	disconnecter, ok := channel.(interface{ Disconnect() error })
	if !ok {
		return nil
	}
	return disconnecter.Disconnect()
}

func openCCIDError(role string, err error) error {
	if ccidUnavailable(err) {
		return fmt.Errorf("open %s CCID context: %w", role, errCCIDUnavailable)
	}
	return fmt.Errorf("open %s CCID context: %w", role, err)
}

func ccidUnavailable(err error) bool {
	if err == nil {
		return false
	}
	text := err.Error()
	return strings.Contains(text, "SCARD_E_NO_SERVICE") || strings.Contains(text, "0x8010001D")
}

func chooseReader(reader *bufio.Reader, w io.Writer, readers []string, requested, label string) (string, error) {
	return chooseReaderWithDefault(reader, w, readers, requested, label, 0, "")
}

func chooseReaderWithDefault(reader *bufio.Reader, w io.Writer, readers []string, requested, label string, defaultIndex int, unavailable string) (string, error) {
	if requested != "" {
		if !contains(readers, requested) {
			return "", fmt.Errorf("%s reader %q is not available", label, requested)
		}
		if requested == unavailable {
			return "", fmt.Errorf("%s reader must be different from source reader %q", label, unavailable)
		}
		return requested, nil
	}
	if len(readers) == 0 {
		return "", errors.New("no CCID readers found")
	}
	if !hasSelectableReader(readers, unavailable) {
		return "", fmt.Errorf("%s reader must be different from source reader %q", label, unavailable)
	}
	if defaultIndex < 0 || defaultIndex >= len(readers) || readers[defaultIndex] == unavailable {
		defaultIndex = firstSelectableReader(readers, unavailable)
	}
	fmt.Fprintf(w, "\nChoose %s reader:\n", label)
	for i := range len(readers) {
		fmt.Fprintln(w, readerDisplayName(i, readers[i]))
	}
	for {
		answer, err := prompt(reader, w, fmt.Sprintf("%s reader [%d]: ", label, defaultIndex+1))
		if err != nil {
			return "", err
		}
		if answer == "" {
			return readers[defaultIndex], nil
		}
		index, err := parsePositiveIndex(answer, len(readers))
		if err != nil {
			fmt.Fprintf(w, "Choose a reader number between 1 and %d.\n", len(readers))
			continue
		}
		if readers[index] == unavailable {
			fmt.Fprintf(w, "%s reader must be different from source reader.\n", label)
			continue
		}
		return readers[index], nil
	}
}

func readerDisplayName(index int, name string) string {
	return fmt.Sprintf("%d. %s", index+1, name)
}

func defaultReaderIndex(readers []string, unavailable string) int {
	if len(readers) > 1 && readers[0] == unavailable {
		return 1
	}
	return 0
}

func hasSelectableReader(readers []string, unavailable string) bool {
	return firstSelectableReader(readers, unavailable) >= 0
}

func firstSelectableReader(readers []string, unavailable string) int {
	for i, reader := range readers {
		if reader != unavailable {
			return i
		}
	}
	return -1
}

func contains(values []string, value string) bool {
	for _, candidate := range values {
		if candidate == value {
			return true
		}
	}
	return false
}

func parsePositiveIndex(answer string, length int) (int, error) {
	index, err := strconv.Atoi(answer)
	if err != nil {
		return 0, err
	}
	if index < 1 || index > length {
		return 0, fmt.Errorf("index %d is out of range", index)
	}
	return index - 1, nil
}

func prompt(reader *bufio.Reader, w io.Writer, label string) (string, error) {
	fmt.Fprint(w, label)
	line, err := reader.ReadString('\n')
	if err != nil && line == "" {
		return "", err
	}
	return strings.TrimSpace(line), nil
}
