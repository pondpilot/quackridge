package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/app"
	"github.com/pondpilot/quackridge/internal/certstore"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/connectors"
	"github.com/pondpilot/quackridge/internal/control"
	"github.com/pondpilot/quackridge/internal/doctor"
	"github.com/pondpilot/quackridge/internal/lifecycle"
	"github.com/pondpilot/quackridge/internal/pairing"
	"github.com/pondpilot/quackridge/internal/secrets"
	"github.com/pondpilot/quackridge/internal/source/filedb"
	"github.com/pondpilot/quackridge/internal/source/mysql"
	"github.com/pondpilot/quackridge/internal/source/odbc"
	"github.com/pondpilot/quackridge/internal/source/postgres"
)

type App struct {
	Stdin   io.Reader
	Stdout  io.Writer
	Stderr  io.Writer
	Context context.Context
}

func Main(args []string) int {
	return (&App{Stdin: os.Stdin, Stdout: os.Stdout, Stderr: os.Stderr}).Run(args)
}

func (a *App) Run(args []string) int {
	if len(args) == 0 {
		a.usage()
		return 2
	}
	var err error
	switch args[0] {
	case "version":
		err = a.version(args[1:])
	case "source":
		err = a.source(args[1:])
	case "certificate":
		err = a.certificate(args[1:])
	case "serve":
		err = a.serve(args[1:])
	case "status":
		err = a.status(args[1:])
	case "doctor":
		err = a.doctor(args[1:])
	case "pair":
		err = a.pair(args[1:])
	default:
		a.usage()
		return 2
	}
	if err != nil {
		fmt.Fprintln(a.Stderr, err)
		return 1
	}
	return 0
}

func (a *App) usage() {
	fmt.Fprintln(a.Stderr, "usage: quackridge <source|certificate|serve|status|doctor|pair|version>")
}

func certificateStore(configPath string) certstore.Store {
	return certstore.Store{Root: filepath.Join(filepath.Dir(configPath), "state-v2", "certificates")}
}

func (a *App) certificate(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: quackridge certificate <import|list|remove>")
	}
	defaultPath, _ := config.DefaultPath()
	flags := flag.NewFlagSet("certificate "+args[0], flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	path := flags.String("config", defaultPath, "configuration path")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args[1:]); err != nil {
		return err
	}
	store := certificateStore(*path)
	var operation string
	var payload any
	switch args[0] {
	case "import":
		if flags.NArg() != 1 {
			return errors.New("usage: quackridge certificate import <pem-file>")
		}
		data, err := os.ReadFile(flags.Arg(0))
		if err != nil {
			return err
		}
		if len(data) > certstore.MaxBundleSize {
			return fmt.Errorf("certificate file exceeds %d KiB", certstore.MaxBundleSize>>10)
		}
		operation, payload = "certificate_import", struct {
			PEM []byte `json:"pem"`
		}{data}
	case "list":
		if flags.NArg() != 0 {
			return errors.New("usage: quackridge certificate list")
		}
		operation, payload = "certificate_list", nil
	case "remove":
		if flags.NArg() != 1 {
			return errors.New("usage: quackridge certificate remove <reference>")
		}
		operation, payload = "certificate_remove", struct {
			Reference string `json:"reference"`
		}{flags.Arg(0)}
	default:
		return errors.New("usage: quackridge certificate <import|list|remove>")
	}
	defaultControl, _ := control.DefaultAddress()
	if filepath.Clean(*path) == filepath.Clean(defaultPath) && control.EndpointPresent(defaultControl) {
		raw, err := json.Marshal(payload)
		if err != nil {
			return err
		}
		response, err := control.Call(context.Background(), defaultControl, control.Request{Operation: operation, Payload: raw})
		if err != nil {
			return &quackridge.Error{Code: quackridge.CodeIncompatible, Message: "live daemon certificate management is incompatible", Cause: err}
		}
		if !response.OK {
			return responseError(response)
		}
		return printCertificates(a.Stdout, response.Certificates, *jsonOutput, args[0])
	}
	var certificates []certstore.Certificate
	switch args[0] {
	case "import":
		certificate, err := store.Import(payload.(struct {
			PEM []byte `json:"pem"`
		}).PEM)
		if err != nil {
			return err
		}
		certificates = []certstore.Certificate{certificate}
	case "list":
		var err error
		certificates, err = store.List()
		if err != nil {
			return err
		}
	case "remove":
		reference := payload.(struct {
			Reference string `json:"reference"`
		}).Reference
		manager := config.TransactionalService{Store: config.Store{Path: *path}, Credentials: secrets.NewLazySystemStore()}
		if filepath.Clean(*path) == filepath.Clean(defaultPath) {
			manager.AfterLock = func(context.Context) error {
				if control.EndpointPresent(defaultControl) {
					return &quackridge.Error{Code: quackridge.CodeIncompatible, Message: "a daemon started while certificate removal was waiting; retry the command"}
				}
				return nil
			}
		}
		if err := manager.WithDocumentLock(context.Background(), func(document config.Document) error {
			if certificateReferenced(document, reference) {
				return errors.New("certificate is referenced by a source")
			}
			return store.Remove(reference)
		}); err != nil {
			return err
		}
	}
	return printCertificates(a.Stdout, certificates, *jsonOutput, args[0])
}

func printCertificates(output io.Writer, certificates []certstore.Certificate, jsonOutput bool, operation string) error {
	if jsonOutput {
		return json.NewEncoder(output).Encode(certificates)
	}
	if operation == "remove" {
		_, err := fmt.Fprintln(output, "certificate removed")
		return err
	}
	for _, certificate := range certificates {
		if _, err := fmt.Fprintf(output, "%s\t%d\n", certificate.Reference, certificate.Size); err != nil {
			return err
		}
	}
	return nil
}

func certificateReferenced(document config.Document, reference string) bool {
	for _, configured := range document.Sources {
		if configured.Type != "postgres" {
			continue
		}
		var options postgres.Config
		if json.Unmarshal(configured.Options, &options) == nil && options.RootCertRef == reference {
			return true
		}
	}
	return false
}

func (a *App) version(args []string) error {
	flags := flag.NewFlagSet("version", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	value := map[string]any{
		"product": quackridge.Product, "product_version": quackridge.Version,
		"protocol_version": quackridge.ProtocolVersion, "duckdb_version": quackridge.DuckDBVersion,
		"capabilities": quackridge.Capabilities(),
	}
	if *jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(value)
	}
	fmt.Fprintf(a.Stdout, "QuackRidge %s (DuckDB %s, protocol %d)\n", quackridge.Version, quackridge.DuckDBVersion, quackridge.ProtocolVersion)
	return nil
}

func consumePairing(ctx context.Context, challenge control.PairingChallenge, origin string) (pairing.Response, error) {
	body, err := json.Marshal(map[string]string{"nonce": challenge.Nonce})
	if err != nil {
		return pairing.Response{}, err
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost, challenge.URL, bytes.NewReader(body))
	if err != nil {
		return pairing.Response{}, err
	}
	request.Header.Set("Origin", origin)
	request.Header.Set("Content-Type", "application/json")
	response, err := http.DefaultClient.Do(request)
	if err != nil {
		return pairing.Response{}, err
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return pairing.Response{}, errors.New("manual pairing failed")
	}
	var paired pairing.Response
	decoder := json.NewDecoder(io.LimitReader(response.Body, 64<<10))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&paired); err != nil {
		return pairing.Response{}, err
	}
	return paired, nil
}

func (a *App) source(args []string) error {
	if len(args) == 0 {
		return errors.New("usage: quackridge source <add|list|test|remove>")
	}
	switch args[0] {
	case "add":
		return a.sourceConfigure(args[1:], "add")
	case "test":
		return a.sourceConfigure(args[1:], "test")
	case "update":
		return a.sourceConfigure(args[1:], "update")
	case "list":
		return a.sourceList(args[1:])
	case "remove":
		return a.sourceRemove(args[1:])
	case "enable":
		return a.sourceSetEnabled(args[1:], true)
	case "disable":
		return a.sourceSetEnabled(args[1:], false)
	default:
		return errors.New("usage: quackridge source <add|test|update|list|remove|enable|disable>")
	}
}

type sourceFlags struct {
	sourceType, configPath, id, name, alias, host, database, user, sslMode, rootCertRef string
	path, dsn, driver, databaseType                                                     string
	properties                                                                          stringMapFlag
	port                                                                                int
	passwordStdin                                                                       bool
	odbcCredentialStdin                                                                 bool
	keepCredential                                                                      bool
	jsonOutput                                                                          bool
}

type stringMapFlag map[string]string

func (values stringMapFlag) String() string { return "" }
func (values stringMapFlag) Set(raw string) error {
	key, value, ok := strings.Cut(raw, "=")
	if !ok || key == "" {
		return errors.New("property must use key=value")
	}
	values[key] = value
	return nil
}

func (a *App) parseSourceFlags(command string, args []string) (sourceFlags, error) {
	if len(args) == 0 || !slices.Contains([]string{"postgres", "mysql", "sqlite", "duckdb", "odbc"}, args[0]) {
		return sourceFlags{}, errors.New("source type must be postgres, mysql, sqlite, duckdb, or odbc")
	}
	sourceType := args[0]
	args = args[1:]
	defaultPath, err := config.DefaultPath()
	if err != nil {
		return sourceFlags{}, err
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	values := sourceFlags{sourceType: sourceType, properties: make(stringMapFlag)}
	flags.StringVar(&values.configPath, "config", defaultPath, "configuration path")
	flags.StringVar(&values.id, "id", "", "source ID")
	flags.StringVar(&values.name, "name", "", "display name")
	flags.StringVar(&values.alias, "alias", "", "DuckDB catalog alias")
	flags.StringVar(&values.host, "host", "", "PostgreSQL host")
	defaultPort, defaultSSLMode := 5432, "require"
	if sourceType == "mysql" {
		defaultPort, defaultSSLMode = 3306, "preferred"
	}
	flags.IntVar(&values.port, "port", defaultPort, "database port")
	flags.StringVar(&values.database, "database", "", "database name")
	flags.StringVar(&values.user, "user", "", "database user")
	flags.StringVar(&values.sslMode, "ssl-mode", defaultSSLMode, "database SSL mode")
	flags.StringVar(&values.rootCertRef, "root-certificate-ref", "", "root certificate reference")
	flags.StringVar(&values.path, "path", "", "absolute SQLite or DuckDB file path")
	flags.StringVar(&values.dsn, "dsn", "", "ODBC data source name")
	flags.StringVar(&values.driver, "driver", "", "ODBC driver name")
	flags.StringVar(&values.databaseType, "database-type", "", "semantic database type")
	flags.Var(values.properties, "property", "ODBC connection property as key=value (repeatable)")
	flags.BoolVar(&values.passwordStdin, "password-stdin", false, "read password from standard input")
	flags.BoolVar(&values.odbcCredentialStdin, "odbc-credential-stdin", false, "read ODBC credential JSON, including secure properties, from standard input")
	flags.BoolVar(&values.keepCredential, "keep-credential", false, "reuse the existing credential during update or test")
	flags.BoolVar(&values.jsonOutput, "json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return sourceFlags{}, err
	}
	if flags.NArg() != 0 {
		return sourceFlags{}, errors.New("unexpected source arguments")
	}
	return values, nil
}

func (a *App) sourceConfigure(args []string, operation string) error {
	values, err := a.parseSourceFlags("source", args)
	if err != nil {
		return err
	}
	var credential []byte
	if values.keepCredential && operation == "add" {
		return errors.New("--keep-credential requires update or test")
	}
	if values.odbcCredentialStdin && (values.sourceType != "odbc" || values.passwordStdin || values.keepCredential) {
		return errors.New("--odbc-credential-stdin is only for ODBC and cannot be combined with --password-stdin")
	}
	if values.odbcCredentialStdin {
		credential, err = io.ReadAll(io.LimitReader(a.Stdin, 64<<10))
		if err != nil || len(bytes.TrimSpace(credential)) == 0 {
			return errors.New("ODBC credential JSON is required on standard input")
		}
		if _, err := odbc.DecodeCredential(credential); err != nil {
			clear(credential)
			return err
		}
	}
	if !values.keepCredential && (values.sourceType == "postgres" || values.sourceType == "mysql" ||
		(values.sourceType == "odbc" && !values.odbcCredentialStdin && (values.user != "" || values.passwordStdin))) {
		credential, err = a.readPassword(values.passwordStdin)
		if err != nil {
			return err
		}
	}
	defer clear(credential)
	var options []byte
	databaseType := values.databaseType
	switch values.sourceType {
	case "postgres":
		options, _ = json.Marshal(postgres.Config{Host: values.host, Port: values.port, Database: values.database, User: values.user, SSLMode: values.sslMode, RootCertRef: values.rootCertRef})
		databaseType = "postgres"
	case "mysql":
		options, _ = json.Marshal(mysql.Config{Host: values.host, Port: values.port, Database: values.database, User: values.user, SSLMode: values.sslMode})
	case "sqlite", "duckdb":
		options, _ = json.Marshal(filedb.Config{Path: values.path})
		databaseType = values.sourceType
	case "odbc":
		if databaseType == "" {
			databaseType = "odbc"
		}
		for key := range values.properties {
			if !odbc.PublicPropertyAllowed(databaseType, key) {
				return fmt.Errorf("ODBC property %q is not public; use --odbc-credential-stdin", key)
			}
		}
		options, _ = json.Marshal(odbc.Config{DSN: values.dsn, Driver: values.driver, Properties: values.properties, DatabaseType: databaseType})
		if len(credential) > 0 && !values.odbcCredentialStdin {
			credential, _ = json.Marshal(odbc.Credential{Username: values.user, Password: string(credential)})
		}
	}
	configured := config.Source{
		ID: values.id, Name: values.name, Alias: values.alias, Type: values.sourceType, DatabaseType: databaseType, Enabled: true,
		Options: options,
	}
	mutation := config.Mutation{Operation: operation, Source: configured, SourceID: values.id, CredentialAction: config.CredentialNone}
	if values.keepCredential {
		mutation.CredentialAction = config.CredentialKeep
	}
	if len(credential) > 0 {
		mutation.CredentialAction, mutation.Credential = config.CredentialReplace, credential
	}
	certificates := certificateStore(values.configPath)
	err = a.applySourceMutation(context.Background(), values.configPath, mutation, connectors.NewWithCertificates(certificates))
	if err != nil {
		return err
	}
	persisted := operation != "test"
	result := map[string]any{"ok": true, "source_id": configured.ID, "persisted": persisted, "operation": operation}
	if values.jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(result)
	}
	verb := map[string]string{"test": "validated", "add": "added", "update": "updated"}[operation]
	fmt.Fprintf(a.Stdout, "source %s %s\n", configured.ID, verb)
	return nil
}

func (a *App) sourceSetEnabled(args []string, enabled bool) error {
	defaultPath, _ := config.DefaultPath()
	flags := flag.NewFlagSet("source enable", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	path := flags.String("config", defaultPath, "configuration path")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: quackridge source <enable|disable> <source-id>")
	}
	mutation := config.Mutation{Operation: "set_enabled", SourceID: flags.Arg(0), Enabled: enabled, CredentialAction: config.CredentialNone}
	if err := a.applySourceMutation(context.Background(), *path, mutation, connectors.NewWithCertificates(certificateStore(*path))); err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{"ok": true, "source_id": flags.Arg(0), "enabled": enabled})
	}
	fmt.Fprintf(a.Stdout, "source %s enabled=%t\n", flags.Arg(0), enabled)
	return nil
}

func (a *App) applySourceMutation(ctx context.Context, path string, mutation config.Mutation, validator config.Validator, provided ...secrets.Store) error {
	defaultConfig, _ := config.DefaultPath()
	defaultControl, _ := control.DefaultAddress()
	isDefault := filepath.Clean(path) == filepath.Clean(defaultConfig)
	if isDefault {
		probeCtx, cancel := context.WithTimeout(ctx, time.Second)
		response, probeErr := control.Call(probeCtx, defaultControl, control.Request{Operation: "handshake"})
		cancel()
		if probeErr == nil && response.OK {
			payload, err := json.Marshal(mutation)
			if err != nil {
				return err
			}
			response, err = control.Call(ctx, defaultControl, control.Request{Operation: "source_" + mutation.Operation, Payload: payload})
			if err != nil {
				return err
			}
			if !response.OK {
				return responseError(response)
			}
			return nil
		}
		if control.EndpointPresent(defaultControl) {
			return &quackridge.Error{Code: quackridge.CodeIncompatible, Message: "a live daemon does not support this management protocol"}
		}
	}
	var credentialStore secrets.Store
	if len(provided) > 0 {
		credentialStore = provided[0]
	}
	if credentialStore == nil && mutation.CredentialAction != config.CredentialNone && !(mutation.Operation == "test" && mutation.CredentialAction == config.CredentialReplace) {
		var err error
		credentialStore, err = secrets.NewSystemStore()
		if err != nil {
			return err
		}
	}
	service := config.TransactionalService{Store: config.Store{Path: path}, Credentials: credentialStore, Validator: validator}
	if isDefault {
		service.AfterLock = func(context.Context) error {
			if control.EndpointPresent(defaultControl) {
				return &quackridge.Error{Code: quackridge.CodeConflict, Message: "daemon started while preparing the offline mutation; retry through management IPC"}
			}
			return nil
		}
	}
	_, _, err := service.Apply(ctx, mutation)
	return err
}

func responseError(response control.Response) error {
	if response.Error != nil {
		return &quackridge.Error{Code: response.Error.Code, Message: response.Error.Message}
	}
	return &quackridge.Error{Code: response.ErrorCode, Message: response.Message}
}

func (a *App) readPassword(fromStdin bool) ([]byte, error) {
	if fromStdin {
		value, err := io.ReadAll(io.LimitReader(a.Stdin, 16<<10))
		value = bytes.TrimRight(value, "\r\n")
		if err != nil || len(value) == 0 {
			return nil, errors.New("password is required on standard input")
		}
		return value, nil
	}
	file, ok := a.Stdin.(*os.File)
	if !ok || !term.IsTerminal(int(file.Fd())) {
		return nil, errors.New("use --password-stdin when standard input is not a terminal")
	}
	fmt.Fprint(a.Stderr, "Database password: ")
	value, err := term.ReadPassword(int(file.Fd()))
	fmt.Fprintln(a.Stderr)
	if err != nil || len(value) == 0 {
		return nil, errors.New("read database password")
	}
	return value, nil
}

func (a *App) sourceList(args []string) error {
	defaultPath, _ := config.DefaultPath()
	flags := flag.NewFlagSet("source list", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	path := flags.String("config", defaultPath, "configuration path")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	document, err := (config.Store{Path: *path}).Load()
	if err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(document.Sources)
	}
	for _, configured := range document.Sources {
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\t%t\n", configured.ID, configured.Type, configured.Alias, configured.Enabled)
	}
	return nil
}

func (a *App) sourceRemove(args []string) error {
	defaultPath, _ := config.DefaultPath()
	flags := flag.NewFlagSet("source remove", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	path := flags.String("config", defaultPath, "configuration path")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	if flags.NArg() != 1 {
		return errors.New("usage: quackridge source remove <source-id>")
	}
	document, err := (config.Store{Path: *path}).Load()
	if err != nil {
		return err
	}
	var credentialStore secrets.Store
	for _, configured := range document.Sources {
		if configured.ID == flags.Arg(0) && configured.CredentialRef != "" {
			credentialStore, err = secrets.NewSystemStore()
			if err != nil {
				return err
			}
			break
		}
	}
	mutation := config.Mutation{Operation: "remove", SourceID: flags.Arg(0), CredentialAction: config.CredentialNone}
	if err := a.applySourceMutation(context.Background(), *path, mutation, connectors.New(), credentialStore); err != nil {
		return err
	}
	if *jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(map[string]any{"ok": true, "source_id": flags.Arg(0)})
	}
	fmt.Fprintf(a.Stdout, "source %s removed\n", flags.Arg(0))
	return nil
}

func (a *App) serve(args []string) error {
	defaultConfig, _ := config.DefaultPath()
	defaultControl, _ := control.DefaultAddress()
	flags := flag.NewFlagSet("serve", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	configPath := flags.String("config", defaultConfig, "configuration path")
	extensions := flags.String("extensions", os.Getenv("QUACKRIDGE_EXTENSION_DIR"), "verified extension directory")
	controlAddress := flags.String("control", defaultControl, "control endpoint")
	credentialProvider := flags.String("credential-provider", "system", "system or environment")
	eventSocket := flags.String("event-socket", "", "private app lifecycle event endpoint")
	lifecycleFD := flags.Int("lifecycle-fd", -1, "inherited parent lifecycle descriptor")
	startupTimeout := flags.Duration("startup-timeout", 60*time.Second, "maximum startup duration")
	jsonOutput := flags.Bool("json", false, "emit JSON readiness")
	if err := flags.Parse(args); err != nil {
		return err
	}
	var credentialStore secrets.Store
	var err error
	switch *credentialProvider {
	case "system":
		credentialStore = secrets.NewLazySystemStore()
	case "environment":
		credentialStore = secrets.Environment{}
	default:
		return errors.New("credential provider must be system or environment")
	}
	if err != nil {
		return err
	}
	if *startupTimeout <= 0 || *startupTimeout > 5*time.Minute {
		return errors.New("startup timeout must be between zero and five minutes")
	}
	runtime, err := app.New(app.StoreLoader{Store: config.Store{Path: *configPath}}, credentialStore)
	if err != nil {
		return err
	}
	service := quackridge.New(runtime)
	logger := slog.New(slog.NewJSONHandler(a.Stderr, nil))
	ctx := a.Context
	stopSignals := func() {}
	if ctx == nil {
		ctx, stopSignals = signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	}
	defer stopSignals()
	ctx, cancelLifecycle, err := lifecycle.ParentContext(ctx, *lifecycleFD)
	if err != nil {
		return err
	}
	defer cancelLifecycle()
	eventCtx, cancelEvent := context.WithTimeout(ctx, 5*time.Second)
	emitter, err := lifecycle.Connect(eventCtx, *eventSocket)
	cancelEvent()
	if err != nil {
		return &quackridge.Error{Code: quackridge.CodeUnavailableHost, Message: "connect private lifecycle channel failed", Cause: err}
	}
	defer emitter.Close()
	store := config.Store{Path: *configPath}
	certificates := certificateStore(*configPath)
	startupManager := config.TransactionalService{Store: store, Credentials: credentialStore, Validator: connectors.NewWithCertificates(certificates)}
	startupGuard, err := startupManager.AcquireStartup(ctx)
	if err != nil {
		return err
	}
	guardHeld := true
	defer func() {
		if guardHeld {
			_ = startupGuard.Close()
		}
	}()
	_ = emitter.Send(lifecycle.Event{Type: "progress", Phase: "starting_engine"})
	startupCtx, cancelStartup := context.WithTimeout(ctx, *startupTimeout)
	err = service.Start(startupCtx, quackridge.Options{ExtensionDir: *extensions, Logger: logger})
	cancelStartup()
	if err != nil {
		_ = emitter.Send(lifecycle.Event{Type: "failure", Phase: "starting_engine", Code: string(quackridge.ClassifyError(err).(*quackridge.Error).Code), Message: "backend startup failed"})
		return err
	}
	_ = emitter.Send(lifecycle.Event{Type: "progress", Phase: "publishing_control"})
	startupManager.Runtime = service
	daemonBackend := &daemon{service: service, runtime: runtime, config: store, certificates: certificates,
		manager: startupManager}
	controlServer, err := control.Start(*controlAddress, daemonBackend)
	if err != nil {
		_ = service.Stop(context.Background())
		return err
	}
	if err := startupGuard.Close(); err != nil {
		_ = controlServer.Close()
		_ = service.Stop(context.Background())
		return err
	}
	guardHeld = false
	status := service.Status()
	daemonInstanceID, pairingGeneration := controlServer.Identity()
	if err := emitter.Send(lifecycle.Event{Type: "readiness", Readiness: &lifecycle.Readiness{
		PID: os.Getpid(), DaemonInstanceID: daemonInstanceID, PairingGeneration: pairingGeneration,
		LifecycleState: string(status.State), Endpoint: status.Endpoint, ControlPath: *controlAddress,
		ProductVersion: quackridge.Version, ManagementProtocolVersion: control.Version,
	}}); err != nil {
		_ = controlServer.Close()
		_ = service.Stop(context.Background())
		return err
	}
	if *jsonOutput {
		_ = json.NewEncoder(a.Stdout).Encode(status)
	} else {
		fmt.Fprintf(a.Stdout, "QuackRidge %s at %s\n", status.State, status.Endpoint)
	}
	<-ctx.Done()
	_ = controlServer.Close()
	daemonBackend.closePairings()
	stopCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return service.Stop(stopCtx)
}

type stringList []string

func (s *stringList) String() string { return strings.Join(*s, ",") }
func (s *stringList) Set(value string) error {
	*s = append(*s, value)
	return nil
}

func (a *App) pair(args []string) error {
	defaultAddress, _ := control.DefaultAddress()
	flags := flag.NewFlagSet("pair", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	address := flags.String("control", defaultAddress, "control endpoint")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	manual := flags.Bool("manual", false, "display the Quack URI and token")
	ttl := flags.Duration("ttl", 2*time.Minute, "pairing lifetime")
	origins := stringList{"https://app.pondpilot.io"}
	flags.Var(&origins, "origin", "allowed PondPilot origin (repeatable)")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := control.Call(ctx, *address, control.Request{
		Version: control.Version, Operation: "pair", Origins: origins,
		TTLSeconds: int(ttl.Seconds()),
	})
	if err != nil {
		return err
	}
	if !response.OK || response.Pairing == nil {
		return &quackridge.Error{Code: response.ErrorCode, Message: response.Message}
	}
	if *manual {
		paired, err := consumePairing(ctx, *response.Pairing, origins[0])
		if err != nil {
			return err
		}
		if *jsonOutput {
			return json.NewEncoder(a.Stdout).Encode(paired)
		}
		fmt.Fprintf(a.Stdout, "Quack URI: %s\nToken: %s\n", paired.Endpoint, paired.Token)
		return nil
	}
	if *jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(response.Pairing)
	}
	fmt.Fprintf(a.Stdout, "Pairing URL: %s\nNonce: %s\nExpires: %s\n",
		response.Pairing.URL, response.Pairing.Nonce, response.Pairing.ExpiresAt.Format(time.RFC3339))
	return nil
}

func (a *App) status(args []string) error {
	defaultAddress, _ := control.DefaultAddress()
	flags := flag.NewFlagSet("status", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	address := flags.String("control", defaultAddress, "control endpoint")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	response, err := control.Call(ctx, *address, control.Request{Version: control.Version, Operation: "status"})
	if err != nil {
		return err
	}
	if !response.OK {
		return &quackridge.Error{Code: response.ErrorCode, Message: response.Message}
	}
	if *jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(response.Status)
	}
	fmt.Fprintf(a.Stdout, "%s\t%s\n", response.Status.State, response.Status.Endpoint)
	return nil
}

func (a *App) doctor(args []string) error {
	defaultConfig, _ := config.DefaultPath()
	defaultControl, _ := control.DefaultAddress()
	flags := flag.NewFlagSet("doctor", flag.ContinueOnError)
	flags.SetOutput(a.Stderr)
	path := flags.String("config", defaultConfig, "configuration path")
	address := flags.String("control", defaultControl, "control endpoint")
	extensions := flags.String("extensions", os.Getenv("QUACKRIDGE_EXTENSION_DIR"), "verified extension directory")
	credentialProvider := flags.String("credential-provider", "system", "system or environment")
	jsonOutput := flags.Bool("json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	report := doctor.Run(ctx, doctor.Options{
		ConfigPath: *path, ControlAddress: *address, ExtensionDir: *extensions,
		CredentialProvider: *credentialProvider,
	})
	if *jsonOutput {
		if err := json.NewEncoder(a.Stdout).Encode(report); err != nil {
			return err
		}
		if !report.OK {
			return errors.New("one or more diagnostic checks failed")
		}
		return nil
	}
	for _, check := range report.Checks {
		fmt.Fprintf(a.Stdout, "%s\t%s\t%s\n", check.Level, check.Name, check.Message)
	}
	if !report.OK {
		return errors.New("one or more diagnostic checks failed")
	}
	return nil
}
