package storage

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"battleworld/protocol"
)

type Store struct {
	mu            sync.Mutex
	baseDir       string
	usersFile     string
	hotFile       string
	checkpointDir string
	users         map[string]protocol.UserProfile
	sessions      map[string]protocol.HotSession
}

func NewStore(baseDir string) (*Store, error) {
	usersFile := filepath.Join(baseDir, "data", "cold", "users.json")
	hotFile := filepath.Join(baseDir, "data", "hot", "sessions.json")
	checkpointDir := filepath.Join(baseDir, "data", "hot", "checkpoints")
	if err := os.MkdirAll(filepath.Dir(usersFile), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(filepath.Dir(hotFile), 0o755); err != nil {
		return nil, err
	}
	if err := os.MkdirAll(checkpointDir, 0o755); err != nil {
		return nil, err
	}

	s := &Store{
		baseDir:       baseDir,
		usersFile:     usersFile,
		hotFile:       hotFile,
		checkpointDir: checkpointDir,
		users:         make(map[string]protocol.UserProfile),
		sessions:      make(map[string]protocol.HotSession),
	}
	if err := s.loadJSON(usersFile, &s.users); err != nil {
		return nil, err
	}
	if err := s.loadJSON(hotFile, &s.sessions); err != nil {
		return nil, err
	}
	return s, nil
}

func (s *Store) Register(username, password string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if username == "" || password == "" {
		return errors.New("用户名和密码不能为空")
	}
	if _, ok := s.users[username]; ok {
		return fmt.Errorf("用户 %q 已存在", username)
	}
	s.users[username] = protocol.UserProfile{
		Username:     username,
		PasswordHash: hashPassword(password),
		LastMap:      "green",
		X:            4,
		Y:            4,
		HP:           protocol.InitHP,
		MaxHP:        protocol.InitHP,
		Attack:       protocol.InitAttack,
		Potions:      protocol.MaxPotions,
		Alive:        true,
	}
	return s.saveUsersLocked()
}

func (s *Store) Authenticate(username, password string) (*protocol.UserProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if username == "" || password == "" {
		return nil, errors.New("用户名和密码不能为空")
	}
	user, ok := s.users[username]
	if !ok {
		return nil, fmt.Errorf("用户 %q 不存在", username)
	}
	if user.PasswordHash != hashPassword(password) {
		return nil, errors.New("密码错误")
	}
	copyUser := user
	return &copyUser, nil
}

func (s *Store) LoadProfile(username string) (*protocol.UserProfile, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	user, ok := s.users[username]
	if !ok {
		return nil, fmt.Errorf("用户 %q 不存在", username)
	}
	copyUser := user
	return &copyUser, nil
}

func (s *Store) SaveProfile(profile protocol.UserProfile) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	existing, ok := s.users[profile.Username]
	if ok && profile.PasswordHash == "" {
		profile.PasswordHash = existing.PasswordHash
	}
	s.users[profile.Username] = profile
	return s.saveUsersLocked()
}

func (s *Store) SaveHotSession(session protocol.HotSession) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.sessions[session.Username] = session
	return writeJSONAtomic(s.hotFile, s.sessions)
}

func (s *Store) DeleteHotSession(username string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	delete(s.sessions, username)
	return writeJSONAtomic(s.hotFile, s.sessions)
}

func (s *Store) SaveCheckpoint(cp protocol.MapCheckpoint) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.checkpointDir, cp.MapID+".json")
	return writeJSONAtomic(path, cp)
}

func (s *Store) LoadCheckpoint(mapID string) (*protocol.MapCheckpoint, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	path := filepath.Join(s.checkpointDir, mapID+".json")
	var cp protocol.MapCheckpoint
	if err := s.loadJSON(path, &cp); err != nil {
		return nil, false
	}
	return &cp, true
}

func (s *Store) loadJSON(path string, target any) error {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return err
	}
	if len(data) == 0 {
		return nil
	}
	return json.Unmarshal(data, target)
}

func (s *Store) saveUsersLocked() error {
	return writeJSONAtomic(s.usersFile, s.users)
}

func writeJSONAtomic(path string, payload any) error {
	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

func hashPassword(password string) string {
	sum := sha256.Sum256([]byte(password))
	return hex.EncodeToString(sum[:])
}
