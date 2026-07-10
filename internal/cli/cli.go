package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	passwordauth "strata-pvr/internal/auth"
	"strata-pvr/internal/config"
	"strata-pvr/internal/database"
	"strata-pvr/internal/legacy"
	"strata-pvr/internal/operator"
	"strata-pvr/internal/programstore"
	"strata-pvr/internal/reservationstore"
	"strata-pvr/internal/rulestore"
	"strata-pvr/internal/scheduler"
	"strata-pvr/internal/schedulestore"
	"strata-pvr/internal/storage"
	"strata-pvr/internal/wui"
)

type paths struct {
	config   string
	database string
}

var resolveRuntimePaths = runtimePaths
var validateRuntimePaths = requireStrataRuntime
var renameMigrationPath = os.Rename
var inspectArchivedMigrationFiles = inspectMigrationFiles

func Run(ctx context.Context, args []string, stdout, stderr io.Writer) error {
	_ = ctx
	p := resolveRuntimePaths()
	if len(args) > 0 && args[0] == "init" {
		return initializeStrata(ctx, stdout)
	}
	if len(args) > 0 && args[0] == "migrate" {
		return migrateChinachu(ctx, args[1:], stdout)
	}
	if len(args) == 0 || args[0] == "help" {
		printHelp(stdout)
		return nil
	}
	if isRuntimeCommand(args) {
		if err := validateRuntimePaths(p); err != nil {
			return err
		}
	}
	switch args[0] {
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
		return programList(p.database, "reservations", args[1:], stdout)
	case "recording":
		return programList(p.database, programstore.Recording, args[1:], stdout)
	case "recorded":
		return programList(p.database, programstore.Recorded, args[1:], stdout)
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

func requireStrataRuntime(p paths) error {
	if _, err := config.Load(p.config); err != nil {
		return fmt.Errorf("Strata runtime is not initialized; run strata-pvr init or migrate: %w", err)
	}
	if _, err := os.Stat(p.database); err != nil {
		return fmt.Errorf("Strata database is unavailable; run strata-pvr init or migrate: %w", err)
	}
	return nil
}

func isRuntimeCommand(args []string) bool {
	if len(args) == 0 || (args[0] == "service" && len(args) == 3 && args[2] == "initscript") {
		return false
	}
	switch args[0] {
	case "service", "reserve", "unreserve", "skip", "unskip", "stop", "rules", "reserves", "recording", "recorded", "cleanup", "update", "search", "rule", "enrule", "disrule", "rmrule":
		return true
	default:
		return false
	}
}

func runtimePaths() paths {
	return paths{
		config:   filepath.Join("data", "config.json"),
		database: filepath.Join("data", "strata.db"),
	}
}

func initializeStrata(ctx context.Context, stdout io.Writer) error {
	configPath := filepath.Join("data", "config.json")
	databasePath := filepath.Join("data", "strata.db")
	for _, path := range []string{configPath, databasePath} {
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
		for _, path := range []string{configPath, databasePath, databasePath + "-wal", databasePath + "-shm"} {
			_ = os.Remove(path)
		}
	}()
	if err := storage.WriteJSONAtomic(configPath, config.DefaultDocument(), true); err != nil {
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
	legacyConfig, err := config.LoadLegacy(legacyConfigPath)
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
	if err := schedulestore.Write(ctx, filepath.Join(tempDir, "strata.db"), migratedSchedule); err != nil {
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
		if err := programstore.Write(ctx, filepath.Join(tempDir, "strata.db"), collection.name, programs); err != nil {
			return err
		}
		counts[collection.name] = len(programs)
	}
	for _, name := range []string{"rules.json", "reserves.json", "recording.json", "recorded.json", "schedule.json"} {
		if err := os.Remove(filepath.Join(tempDir, name)); err != nil && !os.IsNotExist(err) {
			return err
		}
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
	if err := renameMigrationPath("migrate", archivePath); err != nil {
		return fmt.Errorf("archive migration input: %w", err)
	}
	archivedHashes, archivedSizes, err := inspectArchivedMigrationFiles(archivePath)
	if err != nil || !maps.Equal(sourceHashes, archivedHashes) || !maps.Equal(sourceSizes, archivedSizes) {
		_ = renameMigrationPath(archivePath, "migrate")
		if err != nil {
			return fmt.Errorf("verify archived migration input: %w", err)
		}
		return fmt.Errorf("verify archived migration input: source files changed while migrating")
	}
	if err := renameMigrationPath(tempDir, "data"); err != nil {
		_ = renameMigrationPath(archivePath, "migrate")
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

func convertLegacyConfig(old *config.LegacyConfig) (config.Document, []string, error) {
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
	schedule, err := schedulestore.Read(context.Background(), p.database)
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

func programList(databasePath, collection string, args []string, stdout io.Writer) error {
	opts, err := parseSearchArgs(args)
	if err != nil {
		return err
	}
	opts.normalizationForm = loadNormalizationForm("config.json")
	var programs []legacy.Program
	if collection == "reservations" {
		programs, err = reservationstore.Read(context.Background(), databasePath)
	} else {
		programs, err = programstore.Read(context.Background(), databasePath, collection)
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
	writeTable(w, headers, rows)
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
	writeTable(w, headers, rows)
}

func writeTable(w io.Writer, headers []string, rows [][]string) {
	fmt.Fprintln(w, strings.Join(headers, "\t"))
	for _, row := range rows {
		fmt.Fprintln(w, strings.Join(row, "\t"))
	}
}

func writeTransposedTable(w io.Writer, headers []string, row []string) {
	for i, header := range headers {
		value := ""
		if i < len(row) {
			value = row[i]
		}
		fmt.Fprintf(w, "%s\t%s\n", header, value)
	}
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
	rules, err := rulestore.Read(context.Background(), p.database)
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
		return rulestore.Append(context.Background(), p.database, target)
	}
	if opts.remove {
		_, err := rulestore.Delete(context.Background(), p.database, opts.num)
		return err
	}
	_, err = rulestore.Update(context.Background(), p.database, opts.num, target)
	return err
}

func ruleList(p paths, args []string, stdout io.Writer) error {
	opts, _, err := parseRuleArgs(args)
	if err != nil {
		return err
	}
	detail := hasFlag(args, "-detail", "--detail")
	rules, err := rulestore.Read(context.Background(), p.database)
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
		writeTransposedTable(stdout, headers, rows[0])
		return nil
	}
	writeTable(stdout, headers, rows)
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
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("Usage: reserve <pgid>")
	}
	id, rest := args[0], args[1:]
	simulation := hasFlag(rest, "-s", "--simulation")
	oneSeg := hasFlag(rest, "--1seg", "-1seg")
	schedule, err := schedulestore.Read(context.Background(), p.database)
	if err != nil {
		return err
	}
	reserves, err := reservationstore.Read(context.Background(), p.database)
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
	if err := reservationstore.Upsert(context.Background(), p.database, *target); err != nil {
		return err
	}
	fmt.Fprintln(stdout, "reserve:")
	writePretty(stdout, target)
	fmt.Fprintln(stdout, "予約しました。 スケジューラーを実行して競合を確認することをお勧めします")
	return nil
}

func updateReserve(p paths, args []string, stdout io.Writer, mode string) error {
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("Usage: %s <pgid>", mode)
	}
	id, rest := args[0], args[1:]
	simulation := hasFlag(rest, "-s", "--simulation")
	reserves, err := reservationstore.Read(context.Background(), p.database)
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
			if _, err := reservationstore.Delete(context.Background(), p.database, id); err != nil {
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
			if err := reservationstore.Upsert(context.Background(), p.database, reserves[i]); err != nil {
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
			if err := reservationstore.Upsert(context.Background(), p.database, reserves[i]); err != nil {
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
	if len(args) == 0 || strings.HasPrefix(args[0], "-") {
		return fmt.Errorf("Usage: stop <pgid>")
	}
	id, rest := args[0], args[1:]
	simulation := hasFlag(rest, "-s", "--simulation")
	recording, err := programstore.Read(context.Background(), p.database, programstore.Recording)
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
			if err := programstore.Upsert(context.Background(), p.database, programstore.Recording, target); err != nil {
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
	reserves, err := reservationstore.Read(context.Background(), p.database)
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
	return reservationstore.Upsert(context.Background(), p.database, *target)
}

func cleanup(p paths, args []string, stdout io.Writer) error {
	simulation := hasFlag(args, "-s", "--simulation")
	recorded, err := programstore.Read(context.Background(), p.database, programstore.Recorded)
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
	writeTable(stdout, []string{"action", "Program ID", "Recorded"}, rows)
	if simulation {
		return nil
	}
	if len(removed) > 0 {
		for _, id := range removed {
			if err := programstore.Remove(context.Background(), p.database, programstore.Recorded, id); err != nil {
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
				Config:   p.config,
				Database: p.database,
				PID:      filepath.Join("data", "operator.pid"),
				Log:      filepath.Join("log", "operator"),
			}, 0)
		case "scheduler":
			_, err := scheduler.Run(ctx, scheduler.Paths{
				Config:   p.config,
				Database: p.database,
				PID:      filepath.Join("data", "scheduler.pid"),
				Log:      filepath.Join("log", "scheduler"),
			}, false)
			return err
		case "wui":
			return wui.Run(ctx, wui.Paths{
				Config:       p.config,
				Database:     p.database,
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

func prepareServiceRuntimeFor(p paths) error {
	if _, err := os.Stat(p.config); err != nil {
		return fmt.Errorf("Strata is not initialized; run strata-pvr init or migrate: %w", err)
	}
	if err := os.MkdirAll("log", 0o755); err != nil {
		return err
	}
	return os.MkdirAll("data", 0o755)
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

help                    Output this information.

`)
}
