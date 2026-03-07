// Package database provides a lightweight file-based persistent store.
// Two plain-text files are used:
//   data/users.db  — username|password_hash|created_at
//   data/stats.db  — username|wins|losses|kills|deaths|games|last_played
//
// All reads and writes are serialized by a single mutex.
// Writes use an atomic rename strategy (write .tmp then rename) to prevent
// corruption on crash.
package database

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// ── Record types ──────────────────────────────────────────────────────────────

type UserRecord struct {
	Username     string
	PasswordHash string // FNV-1a hex string
	CreatedAt    string // "YYYY-MM-DD HH:MM:SS"
}

type StatsRecord struct {
	Username   string
	Wins       int
	Losses     int
	Kills      int
	Deaths     int
	Games      int
	LastPlayed string // "YYYY-MM-DD HH:MM:SS"
}

// ── Password hashing ──────────────────────────────────────────────────────────

const dbPepper = "warzone2024!"

// fnv1a64 computes the FNV-1a 64-bit hash.
func fnv1a64(s string) uint64 {
	h := uint64(14695981039346656037)
	for _, c := range []byte(s) {
		h ^= uint64(c)
		h *= 1099511628211
	}
	return h
}

// hashPassword returns a hex string of FNV-1a(username:password:pepper).
func hashPassword(username, password string) string {
	salted := username + ":" + password + ":" + dbPepper
	return fmt.Sprintf("%016x", fnv1a64(salted))
}

// ── Database ──────────────────────────────────────────────────────────────────

type Database struct {
	dir        string
	usersFile  string
	statsFile  string
	mu         sync.Mutex
}

// New creates (or opens) a database in the given directory.
func New(dir string) *Database {
	_ = os.MkdirAll(dir, 0o755)
	return &Database{
		dir:       dir,
		usersFile: filepath.Join(dir, "users.db"),
		statsFile: filepath.Join(dir, "stats.db"),
	}
}

// RegisterUser creates a new account. Returns (true, "") on success or
// (false, errMsg) on failure.
func (db *Database) RegisterUser(username, password string) (bool, string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	switch {
	case len(username) == 0 || len(username) > 30:
		return false, "用户名长度须为 1~30 个字符"
	case len(password) < 4:
		return false, "密码至少 4 个字符"
	case strings.ContainsAny(username, "|\n"):
		return false, "用户名含非法字符"
	}

	users := db.loadUsersLocked()
	for _, u := range users {
		if u.Username == username {
			return false, "用户名已被注册"
		}
	}

	now := nowStr()
	users = append(users, UserRecord{
		Username:     username,
		PasswordHash: hashPassword(username, password),
		CreatedAt:    now,
	})
	db.saveUsersLocked(users)

	stats := db.loadStatsLocked()
	stats = append(stats, StatsRecord{Username: username, LastPlayed: now})
	db.saveStatsLocked(stats)

	return true, "注册成功"
}

// Login verifies credentials. Returns (true, "") on success.
func (db *Database) Login(username, password string) (bool, string) {
	db.mu.Lock()
	defer db.mu.Unlock()

	for _, u := range db.loadUsersLocked() {
		if u.Username == username {
			if u.PasswordHash == hashPassword(username, password) {
				return true, "登录成功"
			}
			return false, "密码错误"
		}
	}
	return false, "用户名不存在"
}

// GetStats retrieves the stats record for username.
func (db *Database) GetStats(username string) (StatsRecord, bool) {
	db.mu.Lock()
	defer db.mu.Unlock()

	for _, s := range db.loadStatsLocked() {
		if s.Username == username {
			return s, true
		}
	}
	return StatsRecord{}, false
}

// UpdateStats updates stats after a game ends.
func (db *Database) UpdateStats(username string, isWinner bool, killsThisGame int, died bool) {
	db.mu.Lock()
	defer db.mu.Unlock()

	stats := db.loadStatsLocked()
	found := false
	for i := range stats {
		if stats[i].Username == username {
			stats[i].Games++
			stats[i].Kills += killsThisGame
			if died {
				stats[i].Deaths++
			}
			if isWinner {
				stats[i].Wins++
			} else {
				stats[i].Losses++
			}
			stats[i].LastPlayed = nowStr()
			found = true
			break
		}
	}
	if !found {
		// Fallback: create missing record
		sr := StatsRecord{
			Username:   username,
			Games:      1,
			Kills:      killsThisGame,
			LastPlayed: nowStr(),
		}
		if isWinner {
			sr.Wins = 1
		} else {
			sr.Losses = 1
		}
		if died {
			sr.Deaths = 1
		}
		stats = append(stats, sr)
	}
	db.saveStatsLocked(stats)
}

// Leaderboard returns the top-N players sorted by wins descending.
func (db *Database) Leaderboard(topN int) []StatsRecord {
	db.mu.Lock()
	defer db.mu.Unlock()

	stats := db.loadStatsLocked()
	sort.Slice(stats, func(i, j int) bool {
		return stats[i].Wins > stats[j].Wins
	})
	if len(stats) > topN {
		stats = stats[:topN]
	}
	return stats
}

// ── Internal helpers (must be called with mu held) ────────────────────────────

func (db *Database) loadUsersLocked() []UserRecord {
	f, err := os.Open(db.usersFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	var list []UserRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 3)
		if len(parts) < 3 {
			continue
		}
		list = append(list, UserRecord{
			Username:     parts[0],
			PasswordHash: parts[1],
			CreatedAt:    parts[2],
		})
	}
	return list
}

func (db *Database) saveUsersLocked(list []UserRecord) {
	tmp := db.usersFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	_, _ = fmt.Fprintln(w, "# username|password_hash|created_at")
	for _, u := range list {
		_, _ = fmt.Fprintf(w, "%s|%s|%s\n", u.Username, u.PasswordHash, u.CreatedAt)
	}
	_ = w.Flush()
	_ = f.Close()
	_ = os.Rename(tmp, db.usersFile)
}

func (db *Database) loadStatsLocked() []StatsRecord {
	f, err := os.Open(db.statsFile)
	if err != nil {
		return nil
	}
	defer f.Close()

	var list []StatsRecord
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		parts := strings.SplitN(line, "|", 7)
		if len(parts) < 7 {
			continue
		}
		wins, _   := strconv.Atoi(parts[1])
		losses, _ := strconv.Atoi(parts[2])
		kills, _  := strconv.Atoi(parts[3])
		deaths, _ := strconv.Atoi(parts[4])
		games, _  := strconv.Atoi(parts[5])
		list = append(list, StatsRecord{
			Username:   parts[0],
			Wins:       wins,
			Losses:     losses,
			Kills:      kills,
			Deaths:     deaths,
			Games:      games,
			LastPlayed: parts[6],
		})
	}
	return list
}

func (db *Database) saveStatsLocked(list []StatsRecord) {
	tmp := db.statsFile + ".tmp"
	f, err := os.Create(tmp)
	if err != nil {
		return
	}
	w := bufio.NewWriter(f)
	_, _ = fmt.Fprintln(w, "# username|wins|losses|kills|deaths|games|last_played")
	for _, s := range list {
		_, _ = fmt.Fprintf(w, "%s|%d|%d|%d|%d|%d|%s\n",
			s.Username, s.Wins, s.Losses, s.Kills, s.Deaths, s.Games, s.LastPlayed)
	}
	_ = w.Flush()
	_ = f.Close()
	_ = os.Rename(tmp, db.statsFile)
}

func nowStr() string {
	return time.Now().Format("2006-01-02 15:04:05")
}
