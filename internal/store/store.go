package store

import (
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	_ "modernc.org/sqlite"
)

const databaseName = "ryotunes.sqlite"

// Store is the durable Ryotunes state store.
type Store struct {
	db            *sql.DB
	dirs          Dirs
	log           *slog.Logger
	settings      sync.Map
	settingsWrite sync.Mutex
	now           func() time.Time
}

// Open opens or creates the version-one-compatible database.
func Open(dirs Dirs, log *slog.Logger) (*Store, error) {
	if log == nil {
		log = slog.Default()
	}
	if err := dirs.ensure(); err != nil {
		return nil, err
	}
	dbPath := filepath.Join(dirs.Data, databaseName)
	if err := adoptSiblingDatabase(dirs.Data, dbPath); err != nil {
		return nil, err
	}
	db, err := openDatabase(dbPath)
	if err != nil && corrupt(err) {
		// A damaged database (the daemon was killed mid-write, or the disk
		// failed) would keep Ryotunes from starting at all. Keep it aside for
		// recovery and start with a fresh one.
		aside := dbPath + ".damaged-" + time.Now().Format("20060102-150405")
		if moveErr := moveDatabase(dbPath, aside); moveErr != nil {
			return nil, fmt.Errorf("%w (and could not move it aside: %v)", err, moveErr)
		}
		log.Warn("database was damaged; kept it aside and started a fresh one", "error", err, "kept", aside)
		db, err = openDatabase(dbPath)
	}
	if err != nil {
		return nil, err
	}
	if err := migrate(db); err != nil {
		db.Close()
		return nil, err
	}
	s := &Store{db: db, dirs: dirs, log: log, now: time.Now}
	if err := s.loadSettings(); err != nil {
		db.Close()
		return nil, err
	}
	if err := s.pruneStartupCaches(); err != nil {
		db.Close()
		return nil, err
	}
	return s, nil
}

// openDatabase opens dbPath, creating it if needed, and checks it.
func openDatabase(dbPath string) (*sql.DB, error) {
	file, err := os.OpenFile(dbPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, fmt.Errorf("create database: %w", err)
	}
	if err := file.Close(); err != nil {
		return nil, fmt.Errorf("close database file: %w", err)
	}
	if err := os.Chmod(dbPath, 0o600); err != nil {
		return nil, fmt.Errorf("secure database: %w", err)
	}
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	for _, pragma := range []string{
		"PRAGMA journal_mode=WAL",
		"PRAGMA synchronous=NORMAL",
		"PRAGMA busy_timeout=5000",
	} {
		if _, err := db.Exec(pragma); err != nil {
			db.Close()
			return nil, fmt.Errorf("configure database: %w", err)
		}
	}
	var check string
	if err := db.QueryRow("PRAGMA quick_check").Scan(&check); err != nil {
		db.Close()
		return nil, fmt.Errorf("check database: %w", err)
	}
	if check != "ok" {
		db.Close()
		return nil, fmt.Errorf("check database: %w: %s", errDamaged, check)
	}
	return db, nil
}

var errDamaged = errors.New("database disk image is malformed")

// corrupt reports SQLite finding the database damaged or not a database.
func corrupt(err error) bool {
	if errors.Is(err, errDamaged) {
		return true
	}
	message := strings.ToLower(err.Error())
	return strings.Contains(message, "malformed") || strings.Contains(message, "file is not a database")
}

// moveDatabase renames the database and its WAL files to aside.
func moveDatabase(dbPath, aside string) error {
	if err := os.Rename(dbPath, aside); err != nil {
		return err
	}
	for _, suffix := range []string{"-wal", "-shm"} {
		if _, err := os.Stat(dbPath + suffix); err == nil {
			_ = os.Rename(dbPath+suffix, aside+suffix)
		}
	}
	return nil
}

// Close closes the SQLite connection.
func (s *Store) Close() error {
	return s.db.Close()
}

// Dirs returns the resolved store directories.
func (s *Store) Dirs() Dirs {
	return s.dirs
}

func adoptSiblingDatabase(dataDir, target string) error {
	if _, err := os.Stat(target); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return fmt.Errorf("inspect database: %w", err)
	}
	entries, err := os.ReadDir(dataDir)
	if err != nil {
		return fmt.Errorf("list data directory: %w", err)
	}
	var candidate string
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(strings.ToLower(entry.Name()), ".sqlite") || entry.Name() == databaseName {
			continue
		}
		if candidate != "" {
			return nil
		}
		candidate = filepath.Join(dataDir, entry.Name())
	}
	if candidate == "" {
		return nil
	}
	if err := os.Rename(candidate, target); err != nil {
		return fmt.Errorf("adopt sibling database: %w", err)
	}
	for _, suffix := range []string{"-wal", "-shm", "-journal"} {
		if err := os.Rename(candidate+suffix, target+suffix); err != nil && !os.IsNotExist(err) {
			return fmt.Errorf("adopt sibling database sidecar: %w", err)
		}
	}
	return nil
}

func (s *Store) loadSettings() error {
	rows, err := s.db.Query("SELECT key, value FROM settings")
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			return fmt.Errorf("scan setting: %w", err)
		}
		s.settings.Store(key, value)
	}
	if err := rows.Err(); err != nil {
		return fmt.Errorf("read settings: %w", err)
	}
	return nil
}

func (s *Store) pruneStartupCaches() error {
	now := s.now().Unix()
	if _, err := s.db.Exec("DELETE FROM stream_url_cache WHERE expires_at <= ?", now); err != nil {
		return fmt.Errorf("prune expired streams: %w", err)
	}
	if _, err := s.db.Exec("DELETE FROM plays WHERE video_id LIKE 'LOCAL:%'"); err != nil {
		return fmt.Errorf("prune legacy local plays: %w", err)
	}
	return nil
}
