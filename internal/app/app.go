package app

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"crypto/tls"
	"encoding/base64"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"log"
	"mime"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	webassets "github.com/Mayo-Nas/dockspire/web"
)

// Version is set from the validated release tag at build time; local builds are dev.
var Version = "dev"

type inventorySource interface {
	Snapshot(context.Context) (Inventory, error)
	ProjectContainers(context.Context, string) ([]Container, error)
}

// Run starts the application or executes an offline administration command.
func Run() {
	listen := flag.String("listen", "127.0.0.1:8787", "HTTP(S) listen address; HTTP defaults to loopback, HTTPS to :9443")
	https := flag.Bool("https", false, "Serve HTTPS directly (default listen address :9443)")
	allowHTTP := flag.Bool("allow-http", false, "Allow LAN HTTP on IP addresses (default listen address :8787); use only on a trusted network")
	tlsCert := flag.String("tls-cert", "", "Custom PEM TLS certificate; requires --https and --tls-key")
	tlsKey := flag.String("tls-key", "", "Custom PEM TLS private key")
	tlsHosts := flag.String("tls-hosts", "", "Comma-separated DNS names/IPs for a newly generated certificate; does not make it trusted")
	tlsFingerprint := flag.Bool("tls-fingerprint", false, "Offline: print the public TLS certificate SHA-256 fingerprint and exit")
	browserSetup := flag.Bool("setup", false, "Allow first administrator creation in the browser for five minutes after startup")
	healthcheck := flag.Bool("healthcheck", false, "Check the local HTTP(S) health endpoint and exit")
	socket := flag.String("docker-socket", "/var/run/docker.sock", "Local Docker Unix socket")
	demo := flag.Bool("demo", false, "Use fictional sample data without connecting to Docker")
	dataDir := flag.String("data-dir", "data", "Private stack library directory")
	initAdmin := flag.Bool("init-admin", false, "Offline: initialize admin password from piped stdin and exit")
	resetAdmin := flag.Bool("reset-admin", false, "Offline: reset admin password from piped stdin; stop the server first")
	externalURL := flag.String("external-url", "", "Optional exact HTTP(S) origin; HTTP DNS names require this setting")
	composeBinary := flag.String("compose-binary", "/docker-compose", "Standalone Docker Compose executable")
	portainerRoot := flag.String("portainer-root", "", "Read-only mount of Portainer's compose directory")
	allowImport := flag.Bool("allow-portainer-import", false, "Permit reading Portainer Compose and related environment files")
	importEnvironment := flag.Bool("import-environment", false, "Include Portainer .env and stack.env files when explicitly authorized")
	checkInstallation := flag.Bool("check-installation", false, "Check Docker and Compose availability, print JSON, and exit")
	backup := flag.String("backup", "", "Offline: write a private library ZIP to an unused path outside data-dir")
	exportStack := flag.String("export-stack", "", "Offline: export this stack's selected Compose/environment files; requires --backup")
	restore := flag.String("restore", "", "Offline: restore a library ZIP into a nonexistent data-dir; never contacts Docker")
	historyKeep := flag.Int("history-keep", 200, "Retain this many recent operations per stack, plus protected deployment/recovery records (10-1000)")
	pruneHistory := flag.Bool("prune-history", false, "Offline: apply history retention to existing saved stacks; never contacts Docker")
	hostHelper := flag.Bool("host-helper", false, "Run the restricted host version and package-list service; package installation is disabled")
	hostCheck := flag.Bool("host-check", false, "Inspect host Docker package versions without changing packages")
	hostSimulate := flag.String("host-simulate", "", "Read-only simulation of engine or compose package upgrades")
	hostHelperSocket := flag.String("host-helper-socket", "", "Optional Unix socket for the separately installed host helper")
	flag.Parse()
	if *tlsFingerprint {
		if *initAdmin || *resetAdmin || *backup != "" || *restore != "" || *exportStack != "" || *pruneHistory || *hostHelper || *hostCheck || *hostSimulate != "" || *checkInstallation || *healthcheck {
			log.Fatal("Choose a single offline certificate command")
		}
		fingerprint, err := readCertificateFingerprint(*dataDir, *tlsCert)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println("SHA256 Fingerprint=" + fingerprint)
		return
	}
	if *https && *allowHTTP {
		log.Fatal("Choose --allow-http or --https, not both")
	}
	if *https && strings.HasPrefix(strings.ToLower(*externalURL), "http://") {
		log.Fatal("Use an HTTPS external-url with --https")
	}
	if *allowHTTP && strings.HasPrefix(strings.ToLower(*externalURL), "https://") {
		log.Fatal("Use an HTTP external-url with --allow-http; HTTPS proxies can use --listen without --allow-http")
	}
	if *https || *allowHTTP {
		explicitListen := false
		flag.Visit(func(f *flag.Flag) {
			if f.Name == "listen" {
				explicitListen = true
			}
		})
		if !explicitListen {
			*listen = ":8787"
			if *https {
				*listen = ":9443"
			}
		}
	}
	if *healthcheck {
		if err := checkLocalHealth(*listen, *https); err != nil {
			log.Fatal(err)
		}
		return
	}
	if (*tlsCert != "" || *tlsKey != "") && (!*https || *tlsCert == "" || *tlsKey == "") {
		log.Fatal("Use --https with both --tls-cert and --tls-key")
	}
	if *initAdmin || *resetAdmin {
		if (*initAdmin && *resetAdmin) || *backup != "" || *restore != "" || *exportStack != "" || *pruneHistory || *hostHelper || *hostCheck || *hostSimulate != "" || *checkInstallation {
			log.Fatal("Choose a single offline administrator command")
		}
		if info, err := os.Stdin.Stat(); err != nil || info.Mode()&os.ModeCharDevice != 0 {
			log.Fatal("Pipe the password to stdin; do not enter it in an echoing terminal or command arguments")
		}
		if err := setAdminPassword(*dataDir, os.Stdin, *resetAdmin); err != nil {
			log.Fatal(err)
		}
		log.Print("Administrator password saved. Start the server and sign in as admin.")
		return
	}
	if *historyKeep < 10 || *historyKeep > 1000 {
		log.Fatal("history-keep must be between 10 and 1000")
	}
	if *pruneHistory {
		if *backup != "" || *restore != "" || *exportStack != "" {
			log.Fatal("Choose history maintenance or an archive command")
		}
		library := newLibrary(*dataDir, "", false, nil, nil)
		library.historyKeep = *historyKeep
		if info, err := os.Lstat(library.root); err != nil || !info.IsDir() || info.Mode()&os.ModeSymlink != 0 {
			log.Fatal("Choose an existing library directory")
		}
		records, err := library.list()
		if err != nil {
			log.Fatal(err)
		}
		for _, record := range records {
			library.jobsMu.Lock()
			err = library.pruneHistoryLocked(record.Name)
			library.jobsMu.Unlock()
			if err != nil {
				log.Fatal(err)
			}
		}
		log.Print("Operation history retention applied. Configuration revisions were retained.")
		return
	}
	if *backup != "" || *restore != "" || *exportStack != "" {
		if (*backup != "" && *restore != "") || (*exportStack != "" && *backup == "") {
			log.Fatal("Choose backup/export or restore")
		}
		if *restore != "" {
			if err := restoreLibrary(*restore, *dataDir); err != nil {
				log.Fatal(err)
			}
			return
		}
		library := newLibrary(*dataDir, "", false, nil, nil)
		if err := library.writeArchive(*backup, *exportStack); err != nil {
			log.Fatal(err)
		}
		log.Print("Private archive written. It contains configuration secrets; workload volumes and bind data are not included.")
		return
	}
	if *hostHelper {
		if err := serveHostHelper(); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *hostCheck || *hostSimulate != "" {
		if err := hostInspect(*hostSimulate); err != nil {
			log.Fatal(err)
		}
		return
	}
	var access *adminAccess
	if !*checkInstallation {
		var err error
		access, err = loadServerAccess(*dataDir, *externalURL, *browserSetup)
		if err != nil {
			log.Fatal(err)
		}
		access.directTLS = *https
		access.allowHTTP = *allowHTTP
		access.secure = access.secure || *https
	}
	var source inventorySource = newDockerSource(*socket)
	if *demo {
		source = demoSource{}
	}
	var runner ComposeRunner = commandCompose{binary: *composeBinary, socket: *socket}
	if *demo {
		runner = demoCompose{}
	}
	checkContext, checkCancel := context.WithTimeout(context.Background(), 12*time.Second)
	checks := installationCheck(checkContext, source, runner)
	checkCancel()
	if *checkInstallation {
		_ = json.NewEncoder(os.Stdout).Encode(checks)
		if !checks.Ready && !*demo {
			os.Exit(1)
		}
		return
	}
	for _, check := range checks.Checks {
		log.Printf("Installation check: %s=%s %s", check.Name, check.State, check.Version)
	}
	library := newLibrary(*dataDir, *portainerRoot, *allowImport && !*demo, source, runner)
	library.historyKeep = *historyKeep
	library.includeEnvironment = *importEnvironment
	if *hostHelperSocket != "" && !*demo {
		library.host = newHostHelperClient(*hostHelperSocket)
	}
	server := &http.Server{Addr: *listen, Handler: newApplicationHandler(source, library, access), ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second, WriteTimeout: 190 * time.Second, IdleTimeout: 60 * time.Second}
	if *https {
		certificate, err := serverCertificate(*dataDir, *tlsCert, *tlsKey, strings.Split(*tlsHosts, ",")...)
		if err != nil {
			log.Fatal(err)
		}
		server.TLSConfig = &tls.Config{MinVersion: tls.VersionTLS12, Certificates: []tls.Certificate{certificate}}
		log.Printf("TLS certificate SHA-256 fingerprint: %s", certificateFingerprint(certificate.Certificate[0]))
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go func() {
		<-ctx.Done()
		shutdown, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = server.Shutdown(shutdown)
	}()
	log.Printf("Dockspire listening on %s (demo=%t)", *listen, *demo)
	if required, _ := access.setupState(); required {
		log.Print("Create the administrator in your browser within five minutes. Restart Dockspire if setup times out.")
	}
	serve := server.ListenAndServe
	if *https {
		serve = func() error { return server.ListenAndServeTLS("", "") }
	}
	if err := serve(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}

func newHandler(source inventorySource, libraries ...*stackLibrary) http.Handler {
	var library *stackLibrary
	if len(libraries) > 0 {
		library = libraries[0]
	}
	// Isolated route fixtures use the original localhost guard. Production always
	// constructs an adminAccess before serving requests.
	return newApplicationHandler(source, library, nil)
}

func newApplicationHandler(source inventorySource, library *stackLibrary, access *adminAccess) http.Handler {
	nonce := make([]byte, 32)
	if _, err := rand.Read(nonce); err != nil {
		panic("cannot create request token")
	}
	csrf := base64.RawURLEncoding.EncodeToString(nonce)
	mux := http.NewServeMux()
	registerInstallationRoutes(mux, library, source)
	registerHostClientRoutes(mux, library)
	registerMonitoringRoutes(mux, source)
	mux.HandleFunc("GET /api/session", func(w http.ResponseWriter, r *http.Request) {
		requestCSRF := csrf
		if session, ok := r.Context().Value(adminSessionKey{}).(adminSession); ok {
			requestCSRF = session.csrf
		}
		isDemo := false
		if library != nil {
			_, isDemo = library.runner.(demoCompose)
		}
		importEnabled := library != nil && library.allowImport && library.portainerRoot != "" && !isDemo
		writeJSON(w, http.StatusOK, map[string]any{"csrfToken": requestCSRF, "version": Version, "passwordless": access != nil && access.passwordless(), "stackLibrary": library != nil, "importEnabled": importEnabled, "environmentImportEnabled": importEnabled && library.includeEnvironment, "deployEnabled": library != nil && !isDemo})
	})
	mux.HandleFunc("/api/health", func(w http.ResponseWriter, r *http.Request) {
		capability := "inventory-only"
		if library != nil {
			capability = "stack-library"
		}
		writeJSON(w, http.StatusOK, map[string]string{"status": "ok", "capability": capability})
	})
	mux.HandleFunc("/api/inventory", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		data, err := source.Snapshot(ctx)
		if err != nil {
			writeJSON(w, http.StatusServiceUnavailable, map[string]string{"error": err.Error()})
			return
		}
		if library != nil {
			records, readErr := library.list()
			if readErr != nil {
				writeJSON(w, 503, map[string]string{"error": readErr.Error()})
				return
			}
			for _, record := range records {
				definition := "Saved"
				if record.Unavailable {
					definition = "Unavailable"
					data.Warnings = append(data.Warnings, fmt.Sprintf("Saved configuration for %s is unavailable. Other stacks remain accessible; restore the damaged entry from a backup.", record.Name))
				}
				found := false
				for i := range data.Stacks {
					if data.Stacks[i].Name == record.Name {
						data.Stacks[i].Definition = definition
						data.Stacks[i].Origin = record.Origin
						data.Stacks[i].Managed = record.managed()
						if record.Takeover != nil {
							data.Stacks[i].TakeoverStatus = record.Takeover.State
						}
						data.Stacks[i].Source = record.Origin
						found = true
					}
				}
				if !found {
					state := "draft"
					if record.DeployedAt != "" {
						state = "not running"
					}
					if record.LastDeploymentAttempt != nil && record.DeployedAt == "" {
						state = "needs inspection"
					}
					if record.Unavailable {
						state = "unavailable"
					}
					item := Stack{Name: record.Name, State: state, Source: record.Origin, Definition: definition, Origin: record.Origin, Managed: record.managed()}
					if record.Takeover != nil {
						item.TakeoverStatus = record.Takeover.State
					}
					data.Stacks = append(data.Stacks, item)
				}
			}
		}
		writeJSON(w, http.StatusOK, data)
	})
	if library != nil {
		registerStackRoutes(mux, library)
		registerArchiveRoutes(mux, library)
	}
	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, http.StatusNotFound, map[string]string{"error": "Unknown API endpoint"})
	})
	web := webassets.Assets
	mux.HandleFunc("GET /setup", func(w http.ResponseWriter, r *http.Request) {
		data, _ := webassets.Assets.ReadFile("setup.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	mux.HandleFunc("GET /login", func(w http.ResponseWriter, r *http.Request) {
		data, _ := webassets.Assets.ReadFile("login.html")
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write(data)
	})
	mux.Handle("/", http.FileServer(http.FS(web)))
	var handler http.Handler = mux
	if access != nil {
		handler = access.protect(mux)
	}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Security-Policy", "default-src 'self'; script-src 'self'; style-src 'self'; connect-src 'self'; img-src 'self'; object-src 'none'; base-uri 'none'; frame-ancestors 'none'")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("Referrer-Policy", "no-referrer")
		w.Header().Set("Cache-Control", "no-store")
		allowed := loopbackHost(r.Host)
		if access != nil {
			allowed = access.allowedHost(r)
		}
		if !allowed {
			http.Error(w, "Use the configured Dockspire address or a localhost SSH tunnel", http.StatusForbidden)
			return
		}
		authPost := access != nil && (r.URL.Path == "/api/auth/login" || r.URL.Path == "/api/auth/logout" || r.URL.Path == "/api/auth/setup")
		if r.Method == http.MethodPost && (authPost || (library != nil && (r.URL.Path == "/api/stacks" || strings.HasPrefix(r.URL.Path, "/api/stacks/") || strings.HasPrefix(r.URL.Path, "/api/system/") || strings.HasPrefix(r.URL.Path, "/api/library/")))) {
			media, _, _ := mime.ParseMediaType(r.Header.Get("Content-Type"))
			scheme := "http"
			if r.TLS != nil {
				scheme = "https"
			}
			origin := scheme + "://" + r.Host
			if access != nil {
				origin = access.requestOrigin(r)
			}
			if media != "application/json" || r.Header.Get("Origin") != origin || (access == nil && subtle.ConstantTimeCompare([]byte(r.Header.Get("X-Stacklane-CSRF")), []byte(csrf)) != 1) {
				writeJSON(w, 403, map[string]string{"error": "Open Dockspire in this browser and retry the operation"})
				return
			}
		} else if r.Method != http.MethodGet && r.Method != http.MethodHead {
			w.Header().Set("Allow", "GET, HEAD")
			writeJSON(w, http.StatusMethodNotAllowed, map[string]string{"error": "This milestone supports viewing inventory only"})
			return
		}
		handler.ServeHTTP(w, r)
	})
}

func registerStackRoutes(mux *http.ServeMux, library *stackLibrary) {
	decode := func(w http.ResponseWriter, r *http.Request, target any) bool {
		r.Body = http.MaxBytesReader(w, r.Body, 2*maxDefinition+8192)
		decoder := json.NewDecoder(r.Body)
		decoder.DisallowUnknownFields()
		if decoder.Decode(target) != nil {
			writeJSON(w, 400, map[string]string{"error": "Invalid request or definition too large"})
			return false
		}
		var extra any
		if decoder.Decode(&extra) != io.EOF {
			writeJSON(w, 400, map[string]string{"error": "Use one JSON request"})
			return false
		}
		return true
	}
	respond := func(w http.ResponseWriter, value any, err error) {
		if err != nil {
			writeJSON(w, 400, map[string]string{"error": err.Error()})
			return
		}
		writeJSON(w, 200, publicStackValue(value))
	}
	mux.HandleFunc("GET /api/stacks", func(w http.ResponseWriter, r *http.Request) { records, err := library.list(); respond(w, records, err) })
	mux.HandleFunc("GET /api/stacks/{name}", func(w http.ResponseWriter, r *http.Request) {
		definition, err := library.definition(r.PathValue("name"))
		respond(w, definition, err)
	})
	mux.HandleFunc("GET /api/stacks/{name}/revisions", func(w http.ResponseWriter, r *http.Request) {
		limit := 10
		if raw := r.URL.Query().Get("limit"); raw != "" {
			parsed, err := strconv.Atoi(raw)
			if err != nil || parsed < 1 || parsed > 100 {
				respond(w, nil, fmt.Errorf("Revision page size must be between 1 and 100"))
				return
			}
			limit = parsed
		}
		page, err := library.revisionHistory(r.PathValue("name"), r.URL.Query().Get("cursor"), limit)
		respond(w, page, err)
	})
	mux.HandleFunc("GET /api/stacks/{name}/revisions/{revision}", func(w http.ResponseWriter, r *http.Request) {
		preview, err := library.revisionPreview(r.PathValue("name"), r.PathValue("revision"))
		respond(w, preview, err)
	})
	mux.HandleFunc("POST /api/stacks/{name}/revisions/{revision}/restore", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			RecordVersion string `json:"recordVersion"`
			Confirmation  string `json:"confirmation"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		record, err := library.restoreRevision(ctx, r.PathValue("name"), r.PathValue("revision"), input.RecordVersion, input.Confirmation)
		respond(w, record, err)
	})
	mux.HandleFunc("GET /api/stacks/{name}/revisions/cleanup", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		plan, err := library.reviewRevisionCleanup(ctx, r.PathValue("name"))
		respond(w, plan, err)
	})
	mux.HandleFunc("POST /api/stacks/{name}/revisions/cleanup", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Token        string `json:"token"`
			Confirmation string `json:"confirmation"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		result, err := library.cleanRevisions(ctx, r.PathValue("name"), input.Token, input.Confirmation)
		respond(w, result, err)
	})
	mux.HandleFunc("POST /api/stacks/validate", func(w http.ResponseWriter, r *http.Request) {
		var input stackInput
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		record, err := library.validate(ctx, input)
		respond(w, record, err)
	})
	mux.HandleFunc("POST /api/stacks", func(w http.ResponseWriter, r *http.Request) {
		var input stackInput
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		defer cancel()
		record, err := library.create(ctx, input)
		respond(w, record, err)
	})
	mux.HandleFunc("POST /api/stacks/{name}/preview-import", func(w http.ResponseWriter, r *http.Request) {
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		preview, _, err := library.importPreview(ctx, r.PathValue("name"))
		respond(w, preview, err)
	})
	mux.HandleFunc("POST /api/stacks/{name}/import", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Fingerprint string `json:"fingerprint"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		defer cancel()
		record, err := library.importStack(ctx, r.PathValue("name"), input.Fingerprint)
		respond(w, record, err)
	})
	for _, action := range []string{"edit", "validate-edit"} {
		mux.HandleFunc("POST /api/stacks/{name}/"+action, func(w http.ResponseWriter, r *http.Request) {
			var input editInput
			if !decode(w, r, &input) {
				return
			}
			ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
			defer cancel()
			record, err := library.edit(ctx, r.PathValue("name"), input, action == "edit")
			respond(w, record, err)
		})
	}
	mux.HandleFunc("POST /api/stacks/{name}/review", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			ContainerID string `json:"containerId"`
			Action      string `json:"action,omitempty"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		plan, err := library.reviewAction(ctx, r.PathValue("name"), input.ContainerID, input.Action)
		respond(w, plan, err)
	})
	mux.HandleFunc("POST /api/stacks/{name}/operations", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			Confirmation             string `json:"confirmation"`
			PlanID                   string `json:"planId"`
			PortainerWritersDisabled bool   `json:"portainerWritersDisabled"`
		}
		if !decode(w, r, &input) {
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 25*time.Second)
		defer cancel()
		job, err := library.startJob(ctx, r.PathValue("name"), input.PlanID, input.Confirmation, input.PortainerWritersDisabled)
		if err != nil {
			respond(w, nil, err)
			return
		}
		writeJSON(w, http.StatusAccepted, job)
	})
	mux.HandleFunc("GET /api/stacks/{name}/operations", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("details") == "1" {
			limit := 20
			if raw := r.URL.Query().Get("limit"); raw != "" {
				parsed, err := strconv.Atoi(raw)
				if err != nil || parsed < 1 || parsed > 100 {
					respond(w, nil, fmt.Errorf("History page size must be between 1 and 100"))
					return
				}
				limit = parsed
			}
			history, err := library.historyPage(r.PathValue("name"), r.URL.Query().Get("cursor"), limit)
			respond(w, history, err)
			return
		}
		jobs, err := library.history(r.PathValue("name"))
		respond(w, jobs, err)
	})
	mux.HandleFunc("GET /api/stacks/{name}/operations/{id}", func(w http.ResponseWriter, r *http.Request) {
		job, err := library.job(r.PathValue("id"))
		if err == nil && job.Name != r.PathValue("name") {
			err = fmt.Errorf("Operation not found")
		}
		respond(w, job, err)
	})
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	if err := json.NewEncoder(w).Encode(data); err != nil {
		log.Print(fmt.Errorf("encode response: %w", err))
	}
}
