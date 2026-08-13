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
	"slices"
	"strings"
	"syscall"
	"time"

	"golang.org/x/term"

	quackridge "github.com/pondpilot/quackridge"
	"github.com/pondpilot/quackridge/internal/app"
	"github.com/pondpilot/quackridge/internal/config"
	"github.com/pondpilot/quackridge/internal/control"
	"github.com/pondpilot/quackridge/internal/doctor"
	"github.com/pondpilot/quackridge/internal/pairing"
	"github.com/pondpilot/quackridge/internal/secrets"
	"github.com/pondpilot/quackridge/internal/source"
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
	fmt.Fprintln(a.Stderr, "usage: quackridge <source|serve|status|doctor|pair|version>")
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
		return a.sourceAdd(args[1:], true)
	case "test":
		return a.sourceAdd(args[1:], false)
	case "list":
		return a.sourceList(args[1:])
	case "remove":
		return a.sourceRemove(args[1:])
	default:
		return errors.New("usage: quackridge source <add|list|test|remove>")
	}
}

type sourceFlags struct {
	sourceType, configPath, id, name, alias, host, database, user, sslMode, rootCertRef string
	path, dsn, driver, databaseType                                                     string
	properties                                                                          stringMapFlag
	port                                                                                int
	passwordStdin                                                                       bool
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
	flags.BoolVar(&values.jsonOutput, "json", false, "emit JSON")
	if err := flags.Parse(args); err != nil {
		return sourceFlags{}, err
	}
	if flags.NArg() != 0 {
		return sourceFlags{}, errors.New("unexpected source arguments")
	}
	return values, nil
}

func (a *App) sourceAdd(args []string, persist bool) error {
	values, err := a.parseSourceFlags("source", args)
	if err != nil {
		return err
	}
	var credential []byte
	if values.sourceType == "postgres" || values.sourceType == "mysql" ||
		(values.sourceType == "odbc" && (values.user != "" || values.passwordStdin)) {
		credential, err = a.readPassword(values.passwordStdin)
		if err != nil {
			return err
		}
	}
	defer clear(credential)
	var options []byte
	var validator config.Validator
	databaseType := values.databaseType
	switch values.sourceType {
	case "postgres":
		options, _ = json.Marshal(postgres.Config{Host: values.host, Port: values.port, Database: values.database, User: values.user, SSLMode: values.sslMode, RootCertRef: values.rootCertRef})
		validator, databaseType = postgresValidator{}, "postgres"
	case "mysql":
		options, _ = json.Marshal(mysql.Config{Host: values.host, Port: values.port, Database: values.database, User: values.user, SSLMode: values.sslMode})
		validator = mysqlValidator{}
	case "sqlite", "duckdb":
		options, _ = json.Marshal(filedb.Config{Path: values.path})
		validator, databaseType = fileValidator{connector: values.sourceType}, values.sourceType
	case "odbc":
		if databaseType == "" {
			databaseType = "odbc"
		}
		options, _ = json.Marshal(odbc.Config{DSN: values.dsn, Driver: values.driver, Properties: values.properties, DatabaseType: databaseType})
		validator = odbcValidator{}
		if len(credential) > 0 {
			credential, _ = json.Marshal(odbc.Credential{Username: values.user, Password: string(credential)})
		}
	}
	credentialRef := ""
	if len(credential) > 0 {
		credentialRef = "quackridge/source/" + values.id
	}
	configured := config.Source{
		ID: values.id, Name: values.name, Alias: values.alias, Type: values.sourceType, DatabaseType: databaseType, Enabled: true,
		CredentialRef: credentialRef, Options: options,
	}
	configuration := config.Service{Store: config.Store{Path: values.configPath}, Validator: validator}
	if persist {
		if credentialRef != "" {
			credentialStore, err := secrets.NewSystemStore()
			if err != nil {
				return err
			}
			configuration.Credentials = credentialStore
		}
		err = configuration.Add(context.Background(), configured, credential)
	} else {
		err = configuration.Test(context.Background(), configured, credential)
	}
	if err != nil {
		return err
	}
	result := map[string]any{"ok": true, "source_id": configured.ID, "persisted": persist}
	if values.jsonOutput {
		return json.NewEncoder(a.Stdout).Encode(result)
	}
	verb := "validated"
	if persist {
		verb = "added"
	}
	fmt.Fprintf(a.Stdout, "source %s %s\n", configured.ID, verb)
	return nil
}

type postgresValidator struct{}

func (postgresValidator) Validate(ctx context.Context, configured config.Source, credential []byte) error {
	var options postgres.Config
	decoder := json.NewDecoder(bytes.NewReader(configured.Options))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&options); err != nil {
		return err
	}
	adapter := postgres.New(nil, options, postgres.Credential{Password: string(credential)})
	return adapter.Validate(ctx, source.Definition{
		ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
		ConnectorType: configured.Type, DatabaseType: configured.DatabaseType, Enabled: configured.Enabled,
	})
}

type mysqlValidator struct{}

func (mysqlValidator) Validate(ctx context.Context, configured config.Source, credential []byte) error {
	var options mysql.Config
	if err := json.Unmarshal(configured.Options, &options); err != nil {
		return err
	}
	return mysql.New(nil, options, mysql.Credential{Password: string(credential)}).Validate(ctx, source.Definition{
		ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
		ConnectorType: configured.Type, DatabaseType: configured.DatabaseType, Enabled: configured.Enabled,
	})
}

type fileValidator struct{ connector string }

func (v fileValidator) Validate(ctx context.Context, configured config.Source, _ []byte) error {
	var options filedb.Config
	if err := json.Unmarshal(configured.Options, &options); err != nil {
		return err
	}
	return filedb.New(nil, v.connector, options).Validate(ctx, source.Definition{
		ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
		ConnectorType: configured.Type, DatabaseType: configured.DatabaseType, Enabled: configured.Enabled,
	})
}

type odbcValidator struct{}

func (odbcValidator) Validate(ctx context.Context, configured config.Source, credential []byte) error {
	var options odbc.Config
	if err := json.Unmarshal(configured.Options, &options); err != nil {
		return err
	}
	decoded, err := odbc.DecodeCredential(credential)
	if err != nil {
		return err
	}
	return odbc.New(nil, options, decoded).Validate(ctx, source.Definition{
		ID: configured.ID, Name: configured.Name, Alias: configured.Alias,
		ConnectorType: configured.Type, DatabaseType: configured.DatabaseType, Enabled: configured.Enabled,
	})
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
	service := config.Service{Store: config.Store{Path: *path}, Credentials: credentialStore}
	if err := service.Remove(context.Background(), flags.Arg(0)); err != nil {
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
	if err := service.Start(ctx, quackridge.Options{ExtensionDir: *extensions, Logger: logger}); err != nil {
		return err
	}
	daemonBackend := &daemon{service: service, runtime: runtime, config: config.Store{Path: *configPath}}
	controlServer, err := control.Start(*controlAddress, daemonBackend)
	if err != nil {
		_ = service.Stop(context.Background())
		return err
	}
	status := service.Status()
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
