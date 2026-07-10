package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/mirakurun"
	"strata-pvr/internal/operator"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/rulestore"
	"strata-pvr/internal/scheduler"
	"strata-pvr/internal/schedulestore"
	"strata-pvr/internal/storage"
	"strata-pvr/internal/system"
	"strata-pvr/internal/wui"
)

type paths struct {
	config    string
	database  string
	rules     string
	schedule  string
	reserves  string
	recording string
	recorded  string
}

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	_ = ctx
	args, err := normalizeModeArgs(args)
	if err != nil {
		return err
	}
	p := runtimePaths()
	if len(args) > 0 && args[0] == "init" {
		return initializeStrata(ctx, stdout)
	}
	if len(args) > 0 && (args[0] == "migrate" || args[0] == "migration") {
		return migrateChinachu(ctx, args[1:], stdout)
	}
	if len(args) == 0 || args[0] == "help" {
		printHelp(stdout)
		return nil
	}
	switch args[0] {
	case "installer":
		fmt.Fprintln(stdout, "Strata PVR installer: Node.js/npm modules are not required.")
		fmt.Fprintln(stdout, "Automatic Node-era dependency installation is intentionally not performed; build or install the strata-pvr binary directly.")
		return nil
	case "updater":
		fmt.Fprintln(stdout, "Strata PVR updater: automatic git/service/installer operations are intentionally not performed.")
		fmt.Fprintln(stdout, "Update the repository and rebuild strata-pvr; Node.js/npm modules are not required.")
		return nil
	case "test":
		return testCommand(args[1:], stdout)
	case "ircbot":
		fmt.Fprintln(stdout, "Strata PVR ircbot: the experimental Node-era IRC bot is not implemented in the Go runtime.")
		fmt.Fprintln(stdout, "Use WUI/API or build an external bot against the Go API.")
		return nil
	case "compat":
		return compat(ctx, args[1:], stdout)
	case "service":
		return service(ctx, p, args[1:], stdout)
	case "reserve":
		return reserve(p, args[1:], stdout)
	case "unreserve":
		return updateReserve(p, args[1:], stdout, "unreserve")
	case "skip":
		return updateReserve(p, args[1:], stdout, "skip")
	case "unskip":
		return updateReserve(p, args[1:], stdout, "unskip")
	case "stop":
		return stopRecording(p, args[1:], stdout)
	case "rules":
		return ruleList(p, args[1:], stdout)
	case "reserves":
		return programList(p.reserves, p.database, "reservations", args[1:], stdout)
	case "recording":
		return programList(p.recording, p.database, programstore.Recording, args[1:], stdout)
	case "recorded":
		return programList(p.recorded, p.database, programstore.Recorded, args[1:], stdout)
	case "cleanup":
		return cleanup(p, args[1:], stdout)
	case "update":
		return update(ctx, p, args[1:], stdout)
	case "search":
		return search(p, args[1:], stdout)
	case "rule":
		return ruleCommand(p, args[1:], stdout)
	case "enrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--enable"), stdout)
	case "disrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--disable"), stdout)
	case "rmrule":
		return ruleCommand(p, ruleAliasArgs(args[1:], "--remove"), stdout)
	default:
		printHelp(stdout)
		return nil
	}
}

func runtimePaths() paths {
	configPath := "config.json"
	rulesPath := "rules.json"
	databasePath := ""
	if _, err := os.Stat(filepath.Join("data", "config.json")); err == nil {
		configPath = filepath.Join("data", "config.json")
		rulesPath = filepath.Join("data", "rules.json")
		databasePath = filepath.Join("data", "strata.db")
	}
	return paths{
		config:    configPath,
		database:  databasePath,
		rules:     rulesPath,
		schedule:  filepath.Join("data", "schedule.json"),
		reserves:  filepath.Join("data", "reserves.json"),
		recording: filepath.Join("data", "recording.json"),
		recorded:  filepath.Join("data", "recorded.json"),
	}
}

func initializeStrata(ctx context.Context, stdout io.Writer) error {
	configPath := filepath.Join("data", "config.json")
	rulesPath := filepath.Join("data", "rules.json")
	databasePath := filepath.Join("data", "strata.db")
	for _, path := range []string{configPath, rulesPath, databasePath} {
		if _, err := os.Stat(path); err == nil {
			return fmt.Errorf("Strata data already exists: %s", path)
		} else if !os.IsNotExist(err) {
			return err
		}
	}
	if _, err := os.Stat("config.json"); err == nil {
		return fmt.Errorf("legacy config.json detected; move Chinachu files under migrate/ and run the migration command instead")
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll("data", 0o755); err != nil {
		return err
	}
	complete := false
	defer func() {
		if complete {
			return
		}
		for _, path := range []string{configPath, rulesPath, databasePath, databasePath + "-wal", databasePath + "-shm"} {
			_ = os.Remove(path)
		}
	}()
	if err := storage.WriteJSONAtomic(configPath, config.DefaultDocument(), true); err != nil {
		return err
	}
	if err := storage.WriteJSONAtomic(rulesPath, []any{}, true); err != nil {
		return err
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	complete = true
	fmt.Fprintln(stdout, "Initialized Strata PVR in data/.")
	return nil
}

func migrateChinachu(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) != 0 {
		return fmt.Errorf("Usage: strata-pvr migrate")
	}
	if _, err := os.Stat("data"); err == nil {
		return fmt.Errorf("Strata data already exists: data")
	} else if !os.IsNotExist(err) {
		return err
	}
	legacyConfigPath := filepath.Join("migrate", "config.json")
	legacyConfig, err := config.Load(legacyConfigPath)
	if err != nil {
		return fmt.Errorf("validate %s: %w", legacyConfigPath, err)
	}
	doc, warnings, err := convertLegacyConfig(legacyConfig)
	if err != nil {
		return err
	}
	tempDir, err := os.MkdirTemp(".", ".strata-migrate-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tempDir)
	if err := storage.WriteJSONAtomic(filepath.Join(tempDir, "config.json"), doc, true); err != nil {
		return err
	}
	if err := migrateLegacyJSONFiles(tempDir); err != nil {
		return err
	}
	db, err := database.Open(ctx, filepath.Join(tempDir, "strata.db"))
	if err != nil {
		return err
	}
	var migratedRules []json.RawMessage
	if err := storage.ReadJSON(filepath.Join(tempDir, "rules.json"), &migratedRules, "[]"); err != nil {
		db.Close()
		return err
	}
	if err := database.ReplaceRules(ctx, db, migratedRules); err != nil {
		db.Close()
		return err
	}
	var migratedReservations []legacy.Program
	if err := storage.ReadJSON(filepath.Join(tempDir, "reserves.json"), &migratedReservations, "[]"); err != nil {
		db.Close()
		return err
	}
	reservationDocuments := make([]database.ReservationDocument, 0, len(migratedReservations))
	for _, reservation := range migratedReservations {
		document, err := json.Marshal(reservation)
		if err != nil {
			db.Close()
			return err
		}
		reservationDocuments = append(reservationDocuments, database.ReservationDocument{
			ProgramID: reservation.ID, Start: reservation.Start, End: reservation.End, Document: document,
		})
	}
	if err := database.ReplaceReservations(ctx, db, reservationDocuments); err != nil {
		db.Close()
		return err
	}
	if err := db.Close(); err != nil {
		return err
	}
	var migratedSchedule []legacy.ChannelSchedule
	if err := storage.ReadJSON(filepath.Join(tempDir, "schedule.json"), &migratedSchedule, "[]"); err != nil {
		return err
	}
	if err := schedulestore.Write(ctx, filepath.Join(tempDir, "strata.db"), filepath.Join(tempDir, "schedule.json"), migratedSchedule); err != nil {
		return err
	}
	counts := map[string]int{
		"rules": len(migratedRules), "reservations": len(migratedReservations), "scheduleChannels": len(migratedSchedule),
	}
	for _, channel := range migratedSchedule {
		counts["schedulePrograms"] += len(channel.Programs)
	}
	for _, collection := range []struct {
		name string
		path string
	}{
		{programstore.Recording, filepath.Join(tempDir, "recording.json")},
		{programstore.Recorded, filepath.Join(tempDir, "recorded.json")},
	} {
		var programs []legacy.Program
		if err := storage.ReadJSON(collection.path, &programs, "[]"); err != nil {
			return err
		}
		if err := programstore.Write(ctx, filepath.Join(tempDir, "strata.db"), collection.path, collection.name, programs); err != nil {
			return err
		}
		counts[collection.name] = len(programs)
	}
	sourceHashes, sourceSizes, err := inspectMigrationFiles("migrate")
	if err != nil {
		return err
	}
	backupRoot := "backup"
	if err := os.MkdirAll(backupRoot, 0o755); err != nil {
		return err
	}
	stamp := time.Now().Format("20060102-150405")
	archivePath := filepath.Join(backupRoot, "chinachu-"+stamp)
	if err := os.Rename("migrate", archivePath); err != nil {
		return fmt.Errorf("archive migration input: %w", err)
	}
	archivedHashes, archivedSizes, err := inspectMigrationFiles(archivePath)
	if err != nil || !maps.Equal(sourceHashes, archivedHashes) || !maps.Equal(sourceSizes, archivedSizes) {
		_ = os.Rename(archivePath, "migrate")
		if err != nil {
			return fmt.Errorf("verify archived migration input: %w", err)
		}
		return fmt.Errorf("verify archived migration input: source files changed while migrating")
	}
	if err := os.Rename(tempDir, "data"); err != nil {
		_ = os.Rename(archivePath, "migrate")
		return fmt.Errorf("install Strata data: %w", err)
	}
	manifest := map[string]any{
		"schema": "strata/migration-report", "version": 3,
		"source": archivePath, "completedAt": time.Now().Format(time.RFC3339), "warnings": warnings,
		"imported": counts, "sourceSha256": archivedHashes, "sourceSize": archivedSizes,
	}
	if err := storage.WriteJSONAtomic(filepath.Join(backupRoot, "chinachu-"+stamp+"-report.json"), manifest, true); err != nil {
		fmt.Fprintf(stdout, "Warning: migration report could not be written: %v\n", err)
	}
	fmt.Fprintf(stdout, "Migrated Chinachu data to data/. Original files: %s\n", archivePath)
	for _, warning := range warnings {
		fmt.Fprintf(stdout, "Warning: %s\n", warning)
	}
	return nil
}

func inspectMigrationFiles(root string) (map[string]string, map[string]int64, error) {
	hashes := make(map[string]string)
	sizes := make(map[string]int64)
	err := filepath.WalkDir(root, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if entry.IsDir() {
			return nil
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		sum := sha256.Sum256(data)
		relative, err := filepath.Rel(root, path)
		if err != nil {
			return err
		}
		hashes[filepath.ToSlash(relative)] = fmt.Sprintf("%x", sum)
		sizes[filepath.ToSlash(relative)] = int64(len(data))
		return nil
	})
	if err != nil {
		return nil, nil, fmt.Errorf("inspect migration input: %w", err)
	}
	return hashes, sizes, nil
}

func convertLegacyConfig(old *config.Config) (config.Document, []string, error) {
	doc := config.DefaultDocument()
	doc.Mirakurun.URL = old.EffectiveMirakurunPath()
	doc.Mirakurun.RecordingPriority = old.RecordingPriority
	doc.Mirakurun.ConflictedPriority = old.ConflictedPriority
	doc.Recording.Directory = old.RecordedDir
	doc.Recording.FilenameFormat = old.RecordedFormat
	doc.Recording.LowSpace = config.LowSpaceSettings{
		ThresholdMB: old.StorageLowSpaceThresholdMB, Action: old.StorageLowSpaceAction,
	}
	doc.Services.Excluded = old.ExcludeServices
	doc.Services.Order = old.ServiceOrder
	doc.Advanced = config.AdvancedSettings{NormalizationForm: old.NormalizationForm}
	warnings := []string{}
	if legacyConfigHas(old, "uid", "gid") {
		warnings = append(warnings, "legacy uid/gid settings are not represented in Strata config; configure the service account in the process manager")
	}
	if old.WUIPort != nil {
		doc.Web.ListenAddress = old.WUIHost
		doc.Web.Port = *old.WUIPort
		doc.Web.Authentication.Enabled = len(old.WUIUsers) > 0
	} else if old.WUIOpenServer {
		doc.Web.ListenAddress = old.WUIOpenHost
		doc.Web.Port = old.WUIOpenPort
	} else {
		warnings = append(warnings, "no legacy WUI listener was enabled; the default Strata listener was selected")
	}
	if old.WUIPort != nil && old.WUIOpenServer {
		warnings = append(warnings, "legacy authenticated and public WUI listeners were merged; Strata uses the authenticated listener address and port")
	}
	if legacyConfigHas(old, "wuiTlsKeyPath", "wuiTlsCertPath", "wuiTlsCaPath", "wuiTlsPassphrase", "wuiTlsRequestCert", "wuiTlsRejectUnauthorized") {
		warnings = append(warnings, "legacy WUI TLS settings are not represented in Strata config version 1; configure TLS at a reverse proxy")
	}
	if legacyConfigHas(old, "wuiXFF") {
		warnings = append(warnings, "legacy wuiXFF is not represented in Strata config version 1")
	}
	if legacyConfigHas(old, "wuiAllowCountries") {
		warnings = append(warnings, "legacy wuiAllowCountries is unsupported; enforce geographic access policy outside Strata")
	}
	if legacyConfigHas(old, "wuiMdnsAdvertisement") {
		warnings = append(warnings, "legacy WUI mDNS advertisement is unsupported")
	}
	if legacyConfigHas(old, "operTweeter", "operTweeterAuth", "operTweeterFormat") {
		warnings = append(warnings, "legacy Twitter/Tweeter notification settings are unsupported")
	}
	if legacyConfigHas(old, "schedulerStartCommand", "schedulerEndCommand", "epgStartCommand", "epgEndCommand", "conflictCommand", "recordedCommand", "storageLowSpaceCommand", "storageLowSpaceNotifyTo") {
		warnings = append(warnings, "legacy scheduler, EPG, conflict, or recorded hook commands are not represented in Strata config version 1")
	}
	for _, credential := range old.WUIUsers {
		username, password, ok := strings.Cut(credential, ":")
		if !ok || username == "" || password == "" {
			return config.Document{}, nil, fmt.Errorf("invalid legacy wuiUsers entry for %q", username)
		}
		hash, err := passwordauth.HashPassword(password)
		if err != nil {
			return config.Document{}, nil, err
		}
		doc.Web.Authentication.Users = append(doc.Web.Authentication.Users, config.WebUser{Username: username, PasswordHash: hash})
	}
	return doc, warnings, nil
}

func legacyConfigHas(cfg *config.Config, keys ...string) bool {
	for _, key := range keys {
		if raw, ok := cfg.Raw[key]; ok && string(raw) != "null" && string(raw) != "false" && string(raw) != "[]" && string(raw) != "{}" {
			return true
		}
	}
	return false
}

func migrateLegacyJSONFiles(tempDir string) error {
	type item struct {
		source string
		target string
		value  any
	}
	items := []item{
		{filepath.Join("migrate", "rules.json"), "rules.json", &[]legacy.Rule{}},
		{filepath.Join("migrate", "data", "reserves.json"), "reserves.json", &[]legacy.Program{}},
		{filepath.Join("migrate", "data", "recording.json"), "recording.json", &[]legacy.Program{}},
		{filepath.Join("migrate", "data", "recorded.json"), "recorded.json", &[]legacy.Program{}},
		{filepath.Join("migrate", "data", "schedule.json"), "schedule.json", &[]legacy.ChannelSchedule{}},
	}
	for i := range items {
		entry := &items[i]
		if entry.target == "recording.json" {
			alias := filepath.Join("migrate", "data", "recordings.json")
			if _, err := os.Stat(entry.source); os.IsNotExist(err) {
				entry.source = alias
			}
		}
		if _, err := os.Stat(entry.source); os.IsNotExist(err) {
			if entry.target == "rules.json" {
				if err := storage.WriteJSONAtomic(filepath.Join(tempDir, entry.target), []any{}, true); err != nil {
					return err
				}
			}
			continue
		} else if err != nil {
			return err
		}
		if err := storage.ReadJSON(entry.source, entry.value, ""); err != nil {
			return fmt.Errorf("validate %s: %w", entry.source, err)
		}
		if err := storage.WriteJSONAtomic(filepath.Join(tempDir, entry.target), entry.value, true); err != nil {
			return err
		}
	}
	return nil
}

func testCommand(args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: test <app> [options]")
	}
	fmt.Fprintf(stdout, "Strata PVR test: usr/bin/%s is not executed by the Go runtime.\n", args[0])
	fmt.Fprintln(stdout, "Install and run external tools explicitly; Node.js/npm modules are not required.")
	return nil
}

type searchOptions struct {
	rule              legacy.Rule
	id                string
	normalizationForm string
	simple            bool
	detail            bool
	now               bool
	today             bool
	tomorrow          bool
	num               int
	hasNum            bool
}

func search(p paths, args []string, stdout io.Writer) error {
	opts, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	opts.normalizationForm = loadNormalizationForm(p.config)
	schedule, err := schedulestore.Read(context.Background(), p.database, p.schedule)
	if err != nil {
		return err
	}
	now := time.Now()
	matches := make([]legacy.Program, 0)
	for _, channel := range schedule {
		for _, program := range channel.Programs {
			if searchMatches(opts, program, now) {
				matches = append(matches, program)
			}
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })
	if len(matches) == 0 {
		fmt.Fprintln(stdout, "見つかりません")
		return nil
	}
	writeProgramSearchTable(stdout, matches, opts)
	return nil
}

func programList(path, databasePath, collection string, args []string, stdout io.Writer) error {
	opts, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	opts.normalizationForm = loadNormalizationForm("config.json")
	var programs []legacy.Program
	if databasePath != "" && collection == "reservations" {
		programs, err = reservationstore.Read(context.Background(), databasePath, path)
	} else if databasePath != "" {
		programs, err = programstore.Read(context.Background(), databasePath, path, collection)
	} else {
		err = storage.ReadJSON(path, &programs, "[]")
	}
	if err != nil {
		return err
	}
	now := time.Now()
	matches := make([]legacy.Program, 0, len(programs))
	for _, program := range programs {
		if searchMatches(opts, program, now) {
			matches = append(matches, program)
		}
	}
	sort.SliceStable(matches, func(i, j int) bool { return matches[i].Start < matches[j].Start })
	if len(matches) == 0 {
		fmt.Fprintln(stdout, "見つかりません")
		return nil
	}
	writeProgramListTable(stdout, matches, opts)
	return nil
}

func parseSearchArgs(args []string) (searchOptions, error) {
	ruleOpts, rule, err := parseRuleArgs(args)
	if err != nil {
		return searchOptions{}, err
	}
	opts := searchOptions{
		rule:   rule,
		num:    ruleOpts.num,
		hasNum: ruleOpts.hasNum,
	}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for %s", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "-id", "--id":
			v, err := value()
			if err != nil {
				return opts, err
			}
			opts.id = v
		case "-simple", "--simple":
			opts.simple = true
		case "-detail", "--detail":
			opts.detail = true
		case "-now", "--now":
			opts.now = true
		case "-today", "--today":
			opts.today = true
		case "-tomorrow", "--tomorrow":
			opts.tomorrow = true
		}
	}
	return opts, nil
}

func searchMatches(opts searchOptions, program legacy.Program, now time.Time) bool {
	if opts.id != "" {
		return opts.id == program.ID
	}
	if !legacy.ProgramMatchesRuleForCLIWithNormalization(opts.rule, program, opts.normalizationForm) {
		return false
	}
	start := time.UnixMilli(program.Start).Local()
	end := time.UnixMilli(program.End).Local()
	if opts.now && (now.Before(start) || now.After(end)) {
		return false
	}
	today := midnight(now)
	if opts.today && !sameDay(start, today) {
		return false
	}
	if opts.tomorrow && !sameDay(start, today.AddDate(0, 0, 1)) {
		return false
	}
	return true
}

func midnight(t time.Time) time.Time {
	return time.Date(t.Year(), t.Month(), t.Day(), 0, 0, 0, 0, t.Location())
}

func sameDay(a, b time.Time) bool {
	ay, am, ad := a.Date()
	by, bm, bd := b.Date()
	return ay == by && am == bm && ad == bd
}

func loadNormalizationForm(path string) string {
	cfg, err := config.Load(path)
	if err != nil {
		return ""
	}
	return cfg.NormalizationForm
}

func writeProgramSearchTable(w io.Writer, programs []legacy.Program, opts searchOptions) {
	headers := []string{"#", "Type:CH", "Cat", "Datetime", "Dur", "Title"}
	if !opts.simple || opts.detail {
		headers = insertString(headers, 1, "Program ID")
	}
	if opts.detail {
		headers = insertString(headers, indexOfString(headers, "Cat"), "SID")
		headers = append(headers, "Description")
	}
	rows := make([][]string, 0, len(programs))
	for i, program := range programs {
		if opts.hasNum && i != opts.num {
			continue
		}
		datetimeLayout := "06/01/02 15:04"
		if opts.simple {
			datetimeLayout = "02 15:04"
		}
		row := []string{
			strconv.Itoa(i),
			program.Channel.Type + ":" + program.Channel.Channel,
			program.Category,
			time.UnixMilli(program.Start).Local().Format(datetimeLayout),
			fmt.Sprintf("%dm", program.Seconds/60),
			program.Title,
		}
		if !opts.simple || opts.detail {
			row = insertString(row, 1, program.ID)
		}
		if opts.detail {
			row = insertString(row, indexOfString(headers, "SID"), strconv.FormatInt(program.Channel.SID, 10))
			row = append(row, program.Detail)
		}
		rows = append(rows, row)
	}
	writeLegacyTable(w, headers, rows, opts.simple)
}

func writeProgramListTable(w io.Writer, programs []legacy.Program, opts searchOptions) {
	headers := []string{"#", "Type:CH", "Cat", "By", "Datetime", "Dur", "Title"}
	if !opts.simple || opts.detail {
		headers = insertString(headers, 1, "Program ID")
	}
	if opts.detail {
		headers = insertString(headers, indexOfString(headers, "Cat"), "SID")
		headers = append(headers, "Description")
	}
	rows := make([][]string, 0, len(programs))
	for i, program := range programs {
		if opts.hasNum && i != opts.num {
			continue
		}
		datetimeLayout := "06/01/02 15:04"
		if opts.simple {
			datetimeLayout = "02 15:04"
		}
		row := []string{
			strconv.Itoa(i),
			program.Channel.Type + ":" + program.Channel.Channel,
			program.Category,
			reservationOwner(program),
			time.UnixMilli(program.Start).Local().Format(datetimeLayout),
			fmt.Sprintf("%dm", program.Seconds/60),
			program.Title,
		}
		if !opts.simple || opts.detail {
			row = insertString(row, 1, program.ID)
		}
		if opts.detail {
			row = insertString(row, indexOfString(headers, "SID"), strconv.FormatInt(program.Channel.SID, 10))
			row = append(row, program.Detail)
		}
		rows = append(rows, row)
	}
	writeLegacyTable(w, headers, rows, opts.simple)
}

func writeLegacyTable(w io.Writer, headers []string, rows [][]string, simple bool) {
	widths := legacyTableWidths(headers, rows)
	writeLegacyTableRow(w, headers, widths)
	if !simple {
		separator := make([]string, len(headers))
		for i, width := range widths {
			separator[i] = strings.Repeat("-", width)
		}
		writeLegacyTableRow(w, separator, widths)
	}
	for _, row := range rows {
		writeLegacyTableRow(w, row, widths)
	}
}

func writeLegacyTransposedTable(w io.Writer, headers []string, row []string) {
	rows := make([][]string, 0, len(headers))
	for i, header := range headers {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		rows = append(rows, []string{header, value})
	}
	widths := legacyTableWidths([]string{"", ""}, rows)
	for _, row := range rows {
		writeLegacyTableRow(w, row, widths)
	}
}

func legacyTableWidths(headers []string, rows [][]string) []int {
	widths := make([]int, len(headers))
	for i, header := range headers {
		widths[i] = legacyCellWidth(header)
	}
	for _, row := range rows {
		for i, cell := range row {
			if i >= len(widths) {
				continue
			}
			if width := legacyCellWidth(cell); width > widths[i] {
				widths[i] = width
			}
		}
	}
	return widths
}

func writeLegacyTableRow(w io.Writer, row []string, widths []int) {
	for i := range widths {
		if i > 0 {
			fmt.Fprint(w, "  ")
		}
		cell := ""
		if i < len(row) {
			cell = row[i]
		}
		fmt.Fprint(w, cell)
		if i < len(widths)-1 {
			fmt.Fprint(w, strings.Repeat(" ", widths[i]-legacyCellWidth(cell)))
		}
	}
	fmt.Fprintln(w)
}

func legacyCellWidth(value string) int {
	return len([]rune(value))
}

func reservationOwner(program legacy.Program) string {
	if program.IsManualReserved {
		return "user"
	}
	return "rule"
}

func insertString(values []string, index int, value string) []string {
	if index < 0 || index > len(values) {
		index = len(values)
	}
	values = append(values, "")
	copy(values[index+1:], values[index:])
	values[index] = value
	return values
}

func indexOfString(values []string, value string) int {
	for i, item := range values {
		if item == value {
			return i
		}
	}
	return -1
}

func ruleCommand(p paths, args []string, stdout io.Writer) error {
	opts, rule, err := parseRuleArgs(args)
	if err != nil {
		return err
	}
	rules, err := rulestore.Read(context.Background(), p.database, p.rules)
	if err != nil {
		return err
	}
	var target legacy.Rule
	if opts.hasNum {
		if opts.num < 0 || opts.num >= len(rules) {
			return fmt.Errorf("見つかりません")
		}
		target = rules[opts.num]
	}
	mergeRule(&target, rule)
	cleanRuleDeletionMarkers(&target)
	if opts.enable {
		target.IsDisabled = false
	}
	if opts.disable {
		target.IsDisabled = true
	}
	if isZeroRule(target) && !opts.remove {
		return fmt.Errorf("ルールが空です。一つ以上の条件が必要です。")
	}
	if opts.hasNum {
		if opts.remove {
			rules = append(rules[:opts.num], rules[opts.num+1:]...)
			fmt.Fprintln(stdout, "ルールを削除します")
		} else {
			rules[opts.num] = target
			fmt.Fprintln(stdout, "Rule config:")
			writePretty(stdout, target)
		}
	} else {
		if opts.remove || opts.enable || opts.disable {
			return fmt.Errorf("見つかりません")
		}
		rules = append(rules, target)
		fmt.Fprintln(stdout, "Rule config:")
		writePretty(stdout, target)
	}
	if opts.simulation {
		return nil
	}
	if !opts.hasNum {
		return rulestore.Append(context.Background(), p.database, p.rules, target)
	}
	if opts.remove {
		_, err := rulestore.Delete(context.Background(), p.database, p.rules, opts.num)
		return err
	}
	_, err = rulestore.Update(context.Background(), p.database, p.rules, opts.num, target)
	return err
}

func ruleList(p paths, args []string, stdout io.Writer) error {
	opts, _, err := parseRuleArgs(args)
	if err != nil {
		return err
	}
	detail := hasFlag(args, "-detail", "--detail")
	rules, err := rulestore.Read(context.Background(), p.database, p.rules)
	if err != nil {
		return err
	}
	keys := []string{
		"types", "categories", "channels", "ignore_channels", "reserve_flags",
		"ignore_flags", "hour", "duration", "reserve_titles", "ignore_titles",
		"reserve_descriptions", "ignore_descriptions",
	}
	headers := append([]string{"#"}, keys...)
	rows := [][]string{}
	for i, rule := range rules {
		if opts.hasNum && i != opts.num {
			continue
		}
		row := []string{strconv.Itoa(i)}
		for _, key := range keys {
			row = append(row, ruleListValue(rule, key, detail))
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		fmt.Fprintln(stdout, "見つかりません")
		return nil
	}
	if len(rows) == 1 {
		writeLegacyTransposedTable(stdout, headers, rows[0])
		return nil
	}
	writeLegacyTable(stdout, headers, rows, hasFlag(args, "-simple", "--simple"))
	return nil
}

func ruleListValue(rule legacy.Rule, key string, detail bool) string {
	switch key {
	case "types":
		return ruleStringList(rule.Types, false)
	case "categories":
		return ruleStringList(rule.Categories, false)
	case "channels":
		return ruleStringList(rule.Channels, false)
	case "ignore_channels":
		return ruleStringList(rule.IgnoreChannels, false)
	case "reserve_flags":
		return ruleStringList(rule.ReserveFlags, false)
	case "ignore_flags":
		return ruleStringList(rule.IgnoreFlags, false)
	case "hour":
		if rule.Hour == nil {
			return "-"
		}
		return fmt.Sprintf("%d, %d", rule.Hour.Start, rule.Hour.End)
	case "duration":
		if rule.Duration == nil {
			return "-"
		}
		return fmt.Sprintf("%d, %d", rule.Duration.Min, rule.Duration.Max)
	case "reserve_titles":
		return ruleStringList(rule.ReserveTitles, !detail)
	case "ignore_titles":
		return ruleStringList(rule.IgnoreTitles, !detail)
	case "reserve_descriptions":
		return ruleStringList(rule.ReserveDescriptions, !detail)
	case "ignore_descriptions":
		return ruleStringList(rule.IgnoreDescriptions, !detail)
	default:
		return "-"
	}
}

func ruleStringList(values []string, countOnly bool) string {
	if values == nil {
		return "-"
	}
	if countOnly {
		return fmt.Sprintf("[%d]", len(values))
	}
	return strings.Join(values, ", ")
}

type ruleOptions struct {
	num        int
	hasNum     bool
	enable     bool
	disable    bool
	remove     bool
	simulation bool
}

func parseRuleArgs(args []string) (ruleOptions, legacy.Rule, error) {
	var opts ruleOptions
	var rule legacy.Rule
	for i := 0; i < len(args); i++ {
		arg := args[i]
		value := func() (string, error) {
			if i+1 >= len(args) {
				return "", fmt.Errorf("missing value for %s", arg)
			}
			i++
			return args[i], nil
		}
		switch arg {
		case "-s", "--simulation":
			opts.simulation = true
		case "-en", "--enable":
			opts.enable = true
		case "-dis", "--disable":
			opts.disable = true
		case "-rm", "--remove":
			opts.remove = true
		case "-n", "--num":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			n, err := strconv.Atoi(v)
			if err != nil {
				return opts, rule, err
			}
			opts.num = n
			opts.hasNum = true
		case "-sid", "--service-id":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			sid, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return opts, rule, err
			}
			rule.SID = sid
		case "-type", "--type":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.Types = splitCSV(v)
		case "-ch", "--channel":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.Channels = splitCSV(v)
		case "-^ch", "--ignore-channels":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreChannels = splitCSV(v)
		case "-cat", "--category":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.Categories = splitCSV(v)
		case "-start", "--start":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			start, err := strconv.Atoi(v)
			if err != nil {
				return opts, rule, err
			}
			if rule.Hour == nil {
				rule.Hour = &legacy.RangeRule{End: 24}
			}
			rule.Hour.Start = start
		case "-end", "--end":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			end, err := strconv.Atoi(v)
			if err != nil {
				return opts, rule, err
			}
			if rule.Hour == nil {
				rule.Hour = &legacy.RangeRule{}
			}
			rule.Hour.End = end
		case "-mini", "--minimum":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			minimum, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return opts, rule, err
			}
			if rule.Duration == nil {
				rule.Duration = &legacy.DurationRule{Max: 99999999, HasMax: true}
			}
			rule.Duration.Min = minimum
			rule.Duration.HasMin = true
		case "-maxi", "--maximum":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			maximum, err := strconv.ParseInt(v, 10, 64)
			if err != nil {
				return opts, rule, err
			}
			if rule.Duration == nil {
				rule.Duration = &legacy.DurationRule{HasMin: true}
			}
			rule.Duration.Max = maximum
			rule.Duration.HasMax = true
		case "-title", "--titles":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.ReserveTitles = splitCSV(v)
		case "-^title", "--ignore-titles":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreTitles = splitCSV(v)
		case "-desc", "--descriptions":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.ReserveDescriptions = splitCSV(v)
		case "-^desc", "--ignore-descriptions":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreDescriptions = splitCSV(v)
		case "-flag", "--flags":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.ReserveFlags = splitCSV(v)
		case "-^flag", "--ignore-flags":
			v, err := value()
			if err != nil {
				return opts, rule, err
			}
			rule.IgnoreFlags = splitCSV(v)
		}
	}
	return opts, rule, nil
}

func update(ctx context.Context, p paths, args []string, stdout io.Writer) error {
	simulation := hasFlag(args, "-s", "--simulation")
	result, err := scheduler.Run(ctx, scheduler.Paths{
		Config:   p.config,
		Database: p.database,
		Rules:    p.rules,
		Schedule: p.schedule,
		Reserves: p.reserves,
		PID:      filepath.Join("data", "scheduler.pid"),
		Log:      filepath.Join("log", "scheduler"),
	}, simulation)
	if err != nil {
		return err
	}
	fmt.Fprintln(stdout, "RUNNING SCHEDULER.")
	fmt.Fprintf(stdout, "MATCHES: %d\n", result.Matches)
	fmt.Fprintf(stdout, "DUPLICATES: %d\n", result.Duplicates)
	fmt.Fprintf(stdout, "CONFLICTS: %d\n", result.Conflicts)
	fmt.Fprintf(stdout, "SKIPS: %d\n", result.Skips)
	fmt.Fprintf(stdout, "RESERVES: %d\n", result.Reserves)
	return nil
}

func reserve(p paths, args []string, stdout io.Writer) error {
	id, rest, err := programIDArg(args, "reserve")
	if err != nil {
		return err
	}
	simulation := hasFlag(rest, "-s", "--simulation")
	oneSeg := hasFlag(rest, "--1seg", "-1seg")
	schedule, err := schedulestore.Read(context.Background(), p.database, p.schedule)
	if err != nil {
		return err
	}
	reserves, err := reservationstore.Read(context.Background(), p.database, p.reserves)
	if err != nil {
		return err
	}
	target := legacy.GetProgramByID(id, schedule, nil)
	if target == nil {
		return fmt.Errorf("見つかりません")
	}
	if legacy.GetProgramByID(id, nil, reserves) != nil {
		return fmt.Errorf("既に予約されています")
	}
	target.IsManualReserved = true
	if oneSeg {
		target.OneSeg = true
	}
	if simulation {
		fmt.Fprintln(stdout, "[simulation] reserve:")
		writePretty(stdout, target)
		return nil
	}
	if err := reservationstore.Upsert(context.Background(), p.database, p.reserves, *target); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "reserve:")
	writePretty(stdout, target)
	fmt.Fprintln(stdout, "予約しました。 スケジューラーを実行して競合を確認することをお勧めします")
	return nil
}

func updateReserve(p paths, args []string, stdout io.Writer, mode string) error {
	id, rest, err := programIDArg(args, mode)
	if err != nil {
		return err
	}
	simulation := hasFlag(rest, "-s", "--simulation")
	reserves, err := reservationstore.Read(context.Background(), p.database, p.reserves)
	if err != nil {
		return err
	}
	for i := range reserves {
		if reserves[i].ID != id {
			continue
		}
		switch mode {
		case "unreserve":
			if !reserves[i].IsManualReserved {
				return fmt.Errorf("自動予約された番組は解除できません。自動予約ルールを編集してください")
			}
			target := reserves[i]
			if simulation {
				fmt.Fprintln(stdout, "[simulation] unreserve:")
				writePretty(stdout, target)
				return nil
			}
			if _, err := reservationstore.Delete(context.Background(), p.database, p.reserves, id); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "unreserve:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "予約を解除しました。 ")
			return nil
		case "skip":
			if reserves[i].IsManualReserved {
				return fmt.Errorf("手動予約された番組はスキップできません。予約を解除してください。")
			}
			if reserves[i].IsSkip {
				return fmt.Errorf("既にスキップが有効になっています")
			}
			reserves[i].IsSkip = true
			target := reserves[i]
			if simulation {
				fmt.Fprintln(stdout, "[simulation] skip:")
				writePretty(stdout, target)
				return nil
			}
			if err := reservationstore.Upsert(context.Background(), p.database, p.reserves, reserves[i]); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "skip:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "スキップを有効にしました")
			return nil
		case "unskip":
			if !reserves[i].IsSkip {
				return fmt.Errorf("既にスキップは解除されています")
			}
			target := reserves[i]
			reserves[i].IsSkip = false
			if simulation {
				fmt.Fprintln(stdout, "[simulation] skip:")
				writePretty(stdout, target)
				return nil
			}
			if err := reservationstore.Upsert(context.Background(), p.database, p.reserves, reserves[i]); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "skip:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "スキップを解除しました")
			return nil
		}
	}
	return fmt.Errorf("見つかりません")
}

func stopRecording(p paths, args []string, stdout io.Writer) error {
	id, rest, err := programIDArg(args, "stop")
	if err != nil {
		return err
	}
	simulation := hasFlag(rest, "-s", "--simulation")
	recording, err := programstore.Read(context.Background(), p.database, p.recording, programstore.Recording)
	if err != nil {
		return err
	}
	for i := range recording {
		if recording[i].ID == id {
			recording[i].Abort = true
			target := recording[i]
			if simulation {
				fmt.Fprintln(stdout, "[simulation] stop:")
				writePretty(stdout, target)
				return nil
			}
			if !recording[i].IsManualReserved {
				if err := markReserveSkip(p, recording[i].ID); err != nil {
					return err
				}
			}
			if err := programstore.Upsert(context.Background(), p.database, p.recording, programstore.Recording, target); err != nil {
				return err
			}
			fmt.Fprintln(stdout, "stop:")
			writePretty(stdout, target)
			fmt.Fprintln(stdout, "録画を停止しました。 ")
			return nil
		}
	}
	return fmt.Errorf("見つかりません")
}

func markReserveSkip(p paths, id string) error {
	reserves, err := reservationstore.Read(context.Background(), p.database, p.reserves)
	if err != nil {
		return err
	}
	var target *legacy.Program
	for i := range reserves {
		if reserves[i].ID == id {
			reserves[i].IsSkip = true
			target = &reserves[i]
			break
		}
	}
	if target == nil {
		return nil
	}
	return reservationstore.Upsert(context.Background(), p.database, p.reserves, *target)
}

func cleanup(p paths, args []string, stdout io.Writer) error {
	simulation := hasFlag(args, "-s", "--simulation")
	recorded, err := programstore.Read(context.Background(), p.database, p.recorded, programstore.Recorded)
	if err != nil {
		return err
	}
	rows := make([][]string, 0, len(recorded))
	kept := recorded[:0]
	removed := make([]string, 0)
	for _, program := range recorded {
		if program.Recorded != "" {
			if _, err := os.Stat(filepath.FromSlash(program.Recorded)); err == nil {
				kept = append(kept, program)
				rows = append(rows, []string{"exist", program.ID, program.Recorded})
				continue
			}
		}
		action := "removed"
		if simulation {
			action = "[simulation] removed"
			kept = append(kept, program)
		} else {
			removed = append(removed, program.ID)
		}
		rows = append(rows, []string{action, program.ID, program.Recorded})
	}
	writeLegacyTable(stdout, []string{"action", "Program ID", "Recorded"}, rows, false)
	if simulation {
		return nil
	}
	if len(removed) > 0 {
		if p.database == "" {
			if _, err := storage.BackupFile(p.recorded); err != nil {
				return err
			}
			return programstore.Write(context.Background(), "", p.recorded, programstore.Recorded, kept)
		}
		for _, id := range removed {
			if err := programstore.Remove(context.Background(), p.database, p.recorded, programstore.Recorded, id); err != nil {
				return err
			}
			removePreviewCache(context.Background(), p.database, id)
		}
	}
	return nil
}

func removePreviewCache(ctx context.Context, databasePath, programID string) {
	if databasePath == "" {
		return
	}
	db, err := database.Open(ctx, databasePath)
	if err != nil {
		return
	}
	files, err := database.RemovePreviewCacheForProgram(ctx, db, programID)
	db.Close()
	if err != nil {
		return
	}
	cacheDir := filepath.Join(filepath.Dir(databasePath), ".cache", "previews")
	for _, fileName := range files {
		if filepath.Base(fileName) == fileName {
			_ = os.Remove(filepath.Join(cacheDir, fileName))
		}
	}
}

func dumpJSONFile(path, empty string, stdout io.Writer) error {
	var v any
	if err := storage.ReadJSON(path, &v, empty); err != nil {
		return err
	}
	enc := json.NewEncoder(stdout)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}

func service(ctx context.Context, p paths, args []string, stdout io.Writer) error {
	if len(args) != 2 {
		return fmt.Errorf("Usage: strata-pvr service <name> <action>")
	}
	name, action := args[0], args[1]
	if name != "operator" && name != "scheduler" && name != "wui" {
		return fmt.Errorf("Usage: strata-pvr service <name> <action>")
	}
	switch action {
	case "initscript":
		fmt.Fprint(stdout, serviceInitScript(name))
		return nil
	case "execute":
		if err := prepareServiceRuntimeFor(p); err != nil {
			return err
		}
		switch name {
		case "operator":
			return operator.Run(ctx, operator.Paths{
				Config:    p.config,
				Database:  p.database,
				Reserves:  p.reserves,
				Recording: p.recording,
				Recorded:  p.recorded,
				PID:       filepath.Join("data", "operator.pid"),
				Log:       filepath.Join("log", "operator"),
			}, 0)
		case "scheduler":
			_, err := scheduler.Run(ctx, scheduler.Paths{
				Config:   p.config,
				Database: p.database,
				Rules:    p.rules,
				Schedule: p.schedule,
				Reserves: p.reserves,
				PID:      filepath.Join("data", "scheduler.pid"),
				Log:      filepath.Join("log", "scheduler"),
			}, false)
			return err
		case "wui":
			return wui.Run(ctx, wui.Paths{
				Config:       p.config,
				Database:     p.database,
				Rules:        p.rules,
				Schedule:     p.schedule,
				Reserves:     p.reserves,
				Recording:    p.recording,
				Recorded:     p.recorded,
				WebRoot:      "web",
				LogDir:       "log",
				SchedulerPID: filepath.Join("data", "scheduler.pid"),
				OperatorPID:  filepath.Join("data", "operator.pid"),
			})
		default:
			return fmt.Errorf("Usage: strata-pvr service <name> <action>")
		}
	default:
		return fmt.Errorf("Usage: strata-pvr service <name> <action>")
	}
}

func prepareServiceRuntime() error {
	return prepareServiceRuntimeFor(paths{config: "config.json", rules: "rules.json"})
}

func prepareServiceRuntimeFor(p paths) error {
	if p.config == filepath.Join("data", "config.json") {
		if _, err := os.Stat(p.config); err != nil {
			return fmt.Errorf("Strata is not initialized; run strata-pvr init: %w", err)
		}
		if err := os.MkdirAll("log", 0o755); err != nil {
			return err
		}
		return os.MkdirAll("data", 0o755)
	}
	if err := copyIfMissing("config.sample.json", "config.json"); err != nil {
		return err
	}
	if err := copyIfMissing("rules.sample.json", "rules.json"); err != nil {
		return err
	}
	if err := os.MkdirAll("log", 0o755); err != nil {
		return err
	}
	return os.MkdirAll("data", 0o755)
}

func copyIfMissing(src, dst string) error {
	if _, err := os.Stat(dst); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func serviceInitScript(name string) string {
	strataDir, err := os.Getwd()
	if err != nil {
		strataDir = "."
	}
	strataDir = filepath.ToSlash(strataDir)
	return fmt.Sprintf(`#!/bin/bash
# /etc/

### BEGIN INIT INFO
# Provides:          strata-pvr-%[1]s
# Required-Start:    $local_fs $remote_fs $network $syslog
# Required-Stop:     $local_fs $remote_fs $network $syslog
# Default-Start:     2 3 4 5
# Default-Stop:      0 1 6
# Short-Description: starts the Strata PVR %[1]s
# Description:       starts the Strata PVR %[1]s service (USER=$USER)
### END INIT INFO

PATH=/usr/local/sbin:/usr/local/bin:/sbin:/bin:/usr/sbin:/usr/bin
STRATA_PVR_DIR=%[2]s
DAEMON=${STRATA_PVR_DIR}/strata-pvr
DAEMON_OPTS="service %[1]s execute"
NAME=strata-pvr-%[1]s
USER=$USER
PIDFILE=/var/run/strata-pvr-%[1]s.pid

cd $STRATA_PVR_DIR || exit 1
test -x $DAEMON || exit 0

start () {
  echo -n "Starting ${NAME}: "
  if [ -f $PIDFILE ]; then
    PID=$(cat $PIDFILE)
    if [ -z "$(ps axf | grep ${PID} | grep -v grep)" ]; then
      rm -f $PIDFILE
    else
      echo "${NAME} is already running? (pid=${PID})"
      exit
    fi
  fi
  PID=$(su $USER -c "exec $DAEMON $DAEMON_OPTS < /dev/null > /dev/null 2>&1 & echo \$!")
  if [ -z $PID ]; then
    echo "Failed!"
    exit
  fi
  echo $PID > $PIDFILE
  echo "OK."
}

stop () {
  echo -n "Stopping ${NAME}: "
  if [ -f $PIDFILE ]; then
    PID=$(cat $PIDFILE)
    PGID=$(ps -p $PID -o pgrp | grep -v PGRP)
    kill -QUIT -$(echo $PGID)
    echo "OK."
    rm -f $PIDFILE
  else
    echo "${NAME} is not running? (${PIDFILE} not found)."
  fi
}

status () {
  if [ -f $PIDFILE ]; then
    PID=$(cat $PIDFILE)
    if [ -z "$(ps axf | grep ${PID} | grep -v grep)" ]; then
      echo "${NAME} is dead but ${PIDFILE} exists."
    else
      echo "${NAME} is running."
    fi
  else
    echo "${NAME} is NOT running."
  fi
}

case "$1" in
  start )
    start "$@"
    ;;
  stop )
    stop "$@"
    ;;
  restart )
    stop "$@"
    sleep 3
    start "$@"
    ;;
  status )
    status "$@"
    ;;
  * )
    echo "Usage: $NAME {start|stop|restart|status}" >&2
    exit 1
    ;;
esac

exit 0
`, name, shellQuote(strataDir))
}

func shellQuote(s string) string {
	if s == "" {
		return "''"
	}
	return "'" + strings.ReplaceAll(s, "'", "'\"'\"'") + "'"
}

func legacyWebRootCandidate() string {
	legacyName := "China" + "chu"
	return filepath.Join("..", legacyName, "web")
}

func legacyAssetName(ext string) string {
	return "china" + "chu" + ext
}

func compat(ctx context.Context, args []string, stdout io.Writer) error {
	if len(args) == 0 {
		return fmt.Errorf("Usage: strata-pvr compat <check|doctor|diff|backup|wrapper>")
	}
	switch args[0] {
	case "check", "doctor":
		cfg, cfgErr := config.Load("config.json")
		recordedDirErr := cfgErr
		if cfgErr == nil {
			recordedDirErr = validateWritableDir(cfg.RecordedDir)
		}
		diskErr := cfgErr
		if cfgErr == nil {
			diskErr = validateDiskUsage(cfg.RecordedDir)
		}
		servicesErr, programsErr, tunersErr := validateMirakurun(ctx, cfg, cfgErr)
		checks := []struct {
			name string
			err  error
		}{
			{"config.json", validateJSONObjectFile("config.json")},
			{"rules.json", validateJSONArrayFile("rules.json")},
			{"config.sample.json", validateJSONObjectFile("config.sample.json")},
			{"rules.sample.json", validateJSONArrayFile("rules.sample.json")},
			{"data directory", validateDir("data")},
			{"log directory", validateWritableDir("log")},
			{"recordedDir", recordedDirErr},
			{"data/schedule.json", validateJSONArrayFile(filepath.Join("data", "schedule.json"))},
			{"data/reserves.json", validateJSONArrayFile(filepath.Join("data", "reserves.json"))},
			{"data/recording.json", validateJSONArrayFile(filepath.Join("data", "recording.json"))},
			{"data/recorded.json", validateJSONArrayFile(filepath.Join("data", "recorded.json"))},
			{"WUI static assets", validateWUIStaticAssets()},
			{"available disk space", diskErr},
			{"ffmpeg command", validateCommandAvailable("ffmpeg")},
			{"ffprobe command", validateCommandAvailable("ffprobe")},
			{"Mirakurun services", servicesErr},
			{"Mirakurun programs", programsErr},
			{"Mirakurun tuners", tunersErr},
			{"Node.js runtime", nil},
		}
		failed := false
		for _, check := range checks {
			if check.err != nil {
				failed = true
				fmt.Fprintf(stdout, "NG %s: %v\n", check.name, check.err)
			} else {
				fmt.Fprintf(stdout, "OK %s\n", check.name)
			}
		}
		if cfgErr == nil {
			for _, warning := range compatWarnings(cfg) {
				fmt.Fprintf(stdout, "WARN %s\n", warning)
			}
			if args[0] == "doctor" {
				writeCompatConfigSummary(stdout, cfg)
				writeCompatStateSummary(stdout)
				writeCompatNextSteps(stdout)
				for _, warning := range compatStateWarnings() {
					fmt.Fprintf(stdout, "WARN %s\n", warning)
				}
				for _, warning := range compatDoctorWarnings() {
					fmt.Fprintf(stdout, "WARN %s\n", warning)
				}
			}
		}
		if failed {
			return fmt.Errorf("compat check failed")
		}
		return nil
	case "diff":
		return compatDiff(stdout)
	case "backup":
		return compatBackup(stdout)
	case "wrapper":
		fmt.Fprint(stdout, compatWrapperScript())
		return nil
	default:
		return fmt.Errorf("Usage: strata-pvr compat <check|doctor|diff|backup|wrapper>")
	}
}

func compatDoctorWarnings() []string {
	candidates := []string{"strata-pvr"}
	if runtime.GOOS == "windows" {
		candidates = append([]string{"strata-pvr.exe"}, candidates...)
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err == nil {
			if info.IsDir() {
				return []string{candidate + ": wrapper target is a directory, not an executable file"}
			}
			if runtime.GOOS != "windows" && info.Mode().Perm()&0o111 == 0 {
				return []string{candidate + ": wrapper target exists but is not executable"}
			}
			return nil
		}
		if err != nil && !os.IsNotExist(err) {
			return []string{candidate + ": cannot inspect wrapper target: " + err.Error()}
		}
	}
	return []string{"strata-pvr binary not found in the current directory; generated wrappers and initscripts expect it there"}
}

func writeCompatConfigSummary(stdout io.Writer, cfg *config.Config) {
	wuiPort := "disabled"
	if cfg.WUIPort != nil {
		wuiPort = strconv.Itoa(*cfg.WUIPort)
	}
	openServer := "disabled"
	if cfg.WUIOpenServer {
		openServer = fmt.Sprintf("%s:%d", cfg.WUIOpenHost, cfg.WUIOpenPort)
		if cfg.WUIOpenHost == "" {
			openServer = fmt.Sprintf("auto:%d", cfg.WUIOpenPort)
		}
	}
	fmt.Fprintf(stdout, "CONFIG mirakurunPath=%s\n", cfg.EffectiveMirakurunPath())
	fmt.Fprintf(stdout, "CONFIG recordedDir=%s\n", cfg.RecordedDir)
	if abs, err := filepath.Abs(cfg.RecordedDir); err == nil {
		fmt.Fprintf(stdout, "CONFIG recordedDirResolved=%s\n", abs)
	}
	fmt.Fprintf(stdout, "CONFIG recordedFormat=%s\n", cfg.RecordedFormat)
	fmt.Fprintf(stdout, "CONFIG wui=%s:%s open=%s\n", cfg.WUIHost, wuiPort, openServer)
	fmt.Fprintf(stdout, "CONFIG storageLowSpace=%dMB action=%s\n", cfg.StorageLowSpaceThresholdMB, cfg.StorageLowSpaceAction)
	if cfg.NormalizationForm != "" {
		fmt.Fprintf(stdout, "CONFIG normalizationForm=%s\n", cfg.NormalizationForm)
	}
}

func writeCompatStateSummary(stdout io.Writer) {
	for _, item := range []struct {
		label string
		path  string
	}{
		{"scheduleChannels", filepath.Join("data", "schedule.json")},
		{"reserves", filepath.Join("data", "reserves.json")},
		{"recording", filepath.Join("data", "recording.json")},
		{"recorded", filepath.Join("data", "recorded.json")},
	} {
		count, err := jsonArrayLength(item.path)
		if err != nil {
			continue
		}
		fmt.Fprintf(stdout, "STATE %s=%d\n", item.label, count)
	}
}

func jsonArrayLength(path string) (int, error) {
	var values []any
	if err := storage.ReadJSON(path, &values, ""); err != nil {
		return 0, err
	}
	return len(values), nil
}

func compatStateWarnings() []string {
	recording, err := jsonArrayLength(filepath.Join("data", "recording.json"))
	if err != nil || recording == 0 {
		return nil
	}
	return []string{fmt.Sprintf("active recordings detected: %d; avoid migration, wrapper replacement, or service changes until recording finishes", recording)}
}

func writeCompatNextSteps(stdout io.Writer) {
	for _, step := range []string{
		"compat backup",
		"update -s",
		"reserves",
		"service wui execute",
		"service operator execute",
	} {
		fmt.Fprintf(stdout, "NEXT strata-pvr %s\n", step)
	}
}

func compatWrapperScript() string {
	root, err := os.Getwd()
	if err != nil {
		root = "."
	}
	root = filepath.ToSlash(root)
	return fmt.Sprintf(`#!/bin/bash

STRATA_PVR_DIR=%s
DAEMON=${STRATA_PVR_DIR}/strata-pvr

cd "$STRATA_PVR_DIR" || exit 1
exec "$DAEMON" "$@"
`, shellQuote(root))
}

func compatWarnings(cfg *config.Config) []string {
	warnings := []string{
		"native settings editing: the Go dashboard is intentionally read-only; edit config.json directly or use the legacy-compatible /api/config.json PUT endpoint with care",
	}
	legacySampleUser := "china" + "chu:yoshikawa"
	for _, user := range cfg.WUIUsers {
		if user == "strata:yoshikawa" || user == legacySampleUser {
			warnings = append(warnings, "wuiUsers: sample WUI credential is configured; change it before exposing the authenticated listener")
			break
		}
	}
	if cfg.WUIOpenServer {
		warnings = append(warnings, "wuiOpenServer: unauthenticated WUI listener is enabled; bind it to a trusted network or disable it for authenticated-only access")
	}
	return warnings
}

func compatDiff(stdout io.Writer) error {
	checks := []struct {
		path   string
		pretty bool
		value  any
	}{
		{"rules.json", true, &[]legacy.Rule{}},
		{filepath.Join("data", "schedule.json"), false, &[]legacy.ChannelSchedule{}},
		{filepath.Join("data", "reserves.json"), false, &[]legacy.Program{}},
		{filepath.Join("data", "recording.json"), false, &[]legacy.Program{}},
		{filepath.Join("data", "recorded.json"), false, &[]legacy.Program{}},
	}
	for _, check := range checks {
		raw, err := os.ReadFile(check.path)
		if os.IsNotExist(err) {
			fmt.Fprintf(stdout, "MISSING %s\n", check.path)
			continue
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, check.value); err != nil {
			fmt.Fprintf(stdout, "INVALID %s: %v\n", check.path, err)
			continue
		}
		rendered, err := marshalCompatDiffValue(check.value, check.pretty)
		if err != nil {
			return err
		}
		if string(raw) == string(rendered) {
			fmt.Fprintf(stdout, "OK %s\n", check.path)
		} else {
			fmt.Fprintf(stdout, "DIFF %s original=%d go=%d\n", check.path, len(raw), len(rendered))
		}
	}
	return nil
}

func marshalCompatDiffValue(value any, pretty bool) ([]byte, error) {
	if pretty {
		return json.MarshalIndent(value, "", "  ")
	}
	return json.Marshal(value)
}

func compatBackup(stdout io.Writer) error {
	dir := filepath.Join("backup", "strata-pvr-"+time.Now().Format("20060102150405"))
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := []string{
		"config.json",
		"rules.json",
		filepath.Join("data", "schedule.json"),
		filepath.Join("data", "reserves.json"),
		filepath.Join("data", "recording.json"),
		filepath.Join("data", "recorded.json"),
	}
	copied := 0
	for _, src := range files {
		dst := filepath.Join(dir, filepath.ToSlash(src))
		if err := copyBackupFile(src, dst); err != nil {
			if os.IsNotExist(err) {
				fmt.Fprintf(stdout, "SKIP %s: not found\n", src)
				continue
			}
			return err
		}
		copied++
		fmt.Fprintf(stdout, "BACKUP %s -> %s\n", src, dst)
	}
	if copied == 0 {
		return fmt.Errorf("compat backup failed: no files copied")
	}
	fmt.Fprintf(stdout, "OK backup: %s\n", dir)
	return nil
}

func copyBackupFile(src, dst string) error {
	data, err := os.ReadFile(src)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		return err
	}
	return os.WriteFile(dst, data, 0o644)
}

func validateJSONObjectFile(path string) error {
	var v map[string]any
	return storage.ReadJSON(path, &v, "")
}

func validateJSONArrayFile(path string) error {
	var v []any
	return storage.ReadJSON(path, &v, "")
}

func validateDir(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return err
	}
	if !info.IsDir() {
		return fmt.Errorf("not a directory")
	}
	return nil
}

func validateWritableDir(path string) error {
	if err := validateDir(path); err != nil {
		return err
	}
	f, err := os.CreateTemp(path, ".strata-pvr-compat-*")
	if err != nil {
		return err
	}
	name := f.Name()
	if err := f.Close(); err != nil {
		_ = os.Remove(name)
		return err
	}
	return os.Remove(name)
}

func validateWUIStaticAssets() error {
	candidates := []string{"web", legacyWebRootCandidate()}
	requiredSets := []staticAssetSet{
		{files: []string{"index.html", "app.js", "styles.css"}},
		{
			files: []string{"index.html", legacyAssetName(".js"), legacyAssetName(".css"), "init.js"},
			dirs:  []string{"icons", "lib", "locales", "page"},
		},
	}
	for _, candidate := range candidates {
		info, err := os.Stat(candidate)
		if err != nil || !info.IsDir() {
			continue
		}
		var firstErr error
		for _, set := range requiredSets {
			if err := validateStaticAssetSet(candidate, set); err == nil {
				return nil
			} else if firstErr == nil {
				firstErr = err
			}
		}
		if firstErr != nil {
			return firstErr
		}
	}
	return fmt.Errorf("web directory not found")
}

type staticAssetSet struct {
	files []string
	dirs  []string
}

func validateStaticAssetSet(root string, set staticAssetSet) error {
	for _, file := range set.files {
		info, err := os.Stat(filepath.Join(root, file))
		if err != nil {
			return err
		}
		if info.IsDir() {
			return fmt.Errorf("%s is not a file", filepath.Join(root, file))
		}
	}
	for _, dir := range set.dirs {
		info, err := os.Stat(filepath.Join(root, dir))
		if err != nil {
			return err
		}
		if !info.IsDir() {
			return fmt.Errorf("%s is not a directory", filepath.Join(root, dir))
		}
	}
	return nil
}

func validateDiskUsage(path string) error {
	_, err := system.GetDiskUsage(path)
	return err
}

func validateCommandAvailable(name string) error {
	_, err := exec.LookPath(name)
	return err
}

func validateMirakurun(ctx context.Context, cfg *config.Config, cfgErr error) (servicesErr, programsErr, tunersErr error) {
	if cfgErr != nil {
		return cfgErr, cfgErr, cfgErr
	}
	client, err := mirakurun.New(cfg.EffectiveMirakurunPath())
	if err != nil {
		return err, err, err
	}
	client.UserAgent = mirakurun.StrataUserAgent("cli")
	checkCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	_, servicesErr = client.Services(checkCtx)
	_, programsErr = client.Programs(checkCtx)
	_, tunersErr = client.Tuners(checkCtx)
	return servicesErr, programsErr, tunersErr
}

func hasFlag(args []string, names ...string) bool {
	for _, arg := range args {
		for _, name := range names {
			if arg == name {
				return true
			}
		}
	}
	return false
}

func normalizeModeArgs(args []string) ([]string, error) {
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--mode=") {
			mode := strings.TrimPrefix(arg, "--mode=")
			if mode == "" {
				return nil, fmt.Errorf("missing value for %s", arg)
			}
			out := []string{mode}
			out = append(out, args[:i]...)
			out = append(out, args[i+1:]...)
			return out, nil
		}
		if arg == "-mode" || arg == "--mode" {
			if i+1 >= len(args) {
				return nil, fmt.Errorf("missing value for %s", arg)
			}
			mode := args[i+1]
			out := []string{mode}
			out = append(out, args[:i]...)
			out = append(out, args[i+2:]...)
			return out, nil
		}
	}
	return args, nil
}

func programIDArg(args []string, command string) (string, []string, error) {
	if len(args) == 0 {
		return "", nil, fmt.Errorf("Usage: %s <pgid>", command)
	}
	if !strings.HasPrefix(args[0], "-") {
		return args[0], args[1:], nil
	}
	rest := make([]string, 0, len(args))
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "--id=") {
			id := strings.TrimPrefix(arg, "--id=")
			if id == "" {
				return "", nil, fmt.Errorf("missing value for %s", arg)
			}
			rest = append(rest, args[i+1:]...)
			return id, rest, nil
		}
		if arg == "-id" || arg == "--id" {
			if i+1 >= len(args) {
				return "", nil, fmt.Errorf("missing value for %s", arg)
			}
			id := args[i+1]
			rest = append(rest, args[:i]...)
			rest = append(rest, args[i+2:]...)
			return id, rest, nil
		}
	}
	for i, arg := range args {
		if strings.HasPrefix(arg, "-") {
			continue
		}
		rest = append(rest, args[:i]...)
		rest = append(rest, args[i+1:]...)
		return arg, rest, nil
	}
	return "", nil, fmt.Errorf("Usage: %s <pgid>", command)
}

func firstArg(args []string) string {
	if len(args) == 0 {
		return ""
	}
	return args[0]
}

func ruleAliasArgs(args []string, action string) []string {
	if len(args) == 0 {
		return []string{"-n", "", action}
	}
	out := []string{"-n", args[0], action}
	if len(args) > 1 {
		out = append(out, args[1:]...)
	}
	return out
}

func splitCSV(value string) []string {
	if value == "" {
		return nil
	}
	parts := strings.Split(value, ",")
	out := parts[:0]
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part != "" {
			out = append(out, part)
		}
	}
	return out
}

func mergeRule(dst *legacy.Rule, src legacy.Rule) {
	if src.SID != 0 {
		dst.SID = src.SID
	}
	if src.Types != nil {
		dst.Types = src.Types
	}
	if src.Channels != nil {
		dst.Channels = src.Channels
	}
	if src.IgnoreChannels != nil {
		dst.IgnoreChannels = src.IgnoreChannels
	}
	if src.Category != "" {
		dst.Category = src.Category
	}
	if src.Categories != nil {
		dst.Categories = src.Categories
	}
	if src.Hour != nil {
		dst.Hour = src.Hour
	}
	if src.Duration != nil {
		dst.Duration = src.Duration
	}
	if src.ReserveTitles != nil {
		dst.ReserveTitles = src.ReserveTitles
	}
	if src.IgnoreTitles != nil {
		dst.IgnoreTitles = src.IgnoreTitles
	}
	if src.ReserveDescriptions != nil {
		dst.ReserveDescriptions = src.ReserveDescriptions
	}
	if src.IgnoreDescriptions != nil {
		dst.IgnoreDescriptions = src.IgnoreDescriptions
	}
	if src.ReserveFlags != nil {
		dst.ReserveFlags = src.ReserveFlags
	}
	if src.IgnoreFlags != nil {
		dst.IgnoreFlags = src.IgnoreFlags
	}
	if src.RecordedFormat != "" {
		dst.RecordedFormat = src.RecordedFormat
	}
}

func cleanRuleDeletionMarkers(rule *legacy.Rule) {
	if singleNull(rule.Types) {
		rule.Types = nil
	}
	if singleNull(rule.Channels) {
		rule.Channels = nil
	}
	if singleNull(rule.IgnoreChannels) {
		rule.IgnoreChannels = nil
	}
	if singleNull(rule.Categories) {
		rule.Categories = nil
	}
	if singleNull(rule.ReserveTitles) {
		rule.ReserveTitles = nil
	}
	if singleNull(rule.IgnoreTitles) {
		rule.IgnoreTitles = nil
	}
	if singleNull(rule.ReserveDescriptions) {
		rule.ReserveDescriptions = nil
	}
	if singleNull(rule.IgnoreDescriptions) {
		rule.IgnoreDescriptions = nil
	}
	if singleNull(rule.ReserveFlags) {
		rule.ReserveFlags = nil
	}
	if singleNull(rule.IgnoreFlags) {
		rule.IgnoreFlags = nil
	}
	if rule.Hour != nil && (rule.Hour.Start == -1 || rule.Hour.End == -1) {
		rule.Hour = nil
	}
	if rule.Duration != nil && (rule.Duration.Min == -1 || rule.Duration.Max == -1) {
		rule.Duration = nil
	}
	if rule.RecordedFormat == "null" {
		rule.RecordedFormat = ""
	}
}

func singleNull(values []string) bool {
	return len(values) == 1 && values[0] == "null"
}

func isZeroRule(rule legacy.Rule) bool {
	return rule.SID == 0 &&
		len(rule.Types) == 0 &&
		len(rule.Channels) == 0 &&
		len(rule.IgnoreChannels) == 0 &&
		rule.Category == "" &&
		len(rule.Categories) == 0 &&
		rule.Hour == nil &&
		rule.Duration == nil &&
		len(rule.ReserveTitles) == 0 &&
		len(rule.IgnoreTitles) == 0 &&
		len(rule.ReserveDescriptions) == 0 &&
		len(rule.IgnoreDescriptions) == 0 &&
		len(rule.ReserveFlags) == 0 &&
		len(rule.IgnoreFlags) == 0 &&
		rule.RecordedFormat == ""
}

func writePretty(w io.Writer, v any) {
	b, _ := json.MarshalIndent(v, "", "  ")
	fmt.Fprintln(w, string(b))
}

func printHelp(w io.Writer) {
	fmt.Fprint(w, `
Usage: strata-pvr <cmd> ...

Commands:

init                    Initialize a new Strata installation in data/.
migrate                 Convert migrate/ Chinachu files into a Strata installation.
migration               Alias of migrate.
installer               Run a Installer.
updater                 Run a Updater.
service <name> <action> Service-utility.

update                  Run a Scheduler.
search [options]        Search for programs.

reserve <pgid>          Reserve the program manually.
unreserve <pgid>        Unreserve the program manually.
skip <pgid>             Skip the auto-reserved program.
unskip <pgid>           Cancel the skip.
stop <pgid>             Stop the recording.

rule [options]          Add or config a rule for auto reservation.
enrule <rule#>          Enable a rule. (alias of rule -n <rule#> --enable)
disrule <rule#>         Disable a rule. (alias of rule -n <rule#> --disable)
rmrule <rule#>          Remove a rule. (alias of rule -n <rule#> --remove)

rules                   Show a list of auto reserving rules.
reserves                Show a list of reserved programs.
recording               Show a list of recording programs.
recorded                Show a list of recorded programs.

cleanup                 Clean-up the recorded list.

compat <check|doctor|diff|backup|wrapper>
                        Check or back up Strata PVR compatibility state.

ircbot [options]        Connect to IRC server and run a ircbot. (Experimental)

test <app> [options]    Run <app> in usr/bin

help                    Output this information.

`)
}
