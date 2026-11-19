package core

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultTranslationsMetadataURL = "https://metadata.luckperms.net/data/translations"
	defaultTranslationsDownloadURL = "https://metadata.luckperms.net/translation/"
	maxTranslationBundleSize       = 1 << 20 // 1 MiB
)

// TranslationLanguageInfo describes an available LuckPerms community translation.
type TranslationLanguageInfo struct {
	ID           string
	Name         string
	LocaleTag    string
	Progress     int
	Contributors []string
}

// TranslationManager loads bundled English, repository, and custom `.properties` translation bundles.
type TranslationManager struct {
	lp          *LuckPerms
	customDir   string
	repoDir     string
	statusPath  string
	metadataURL string
	downloadURL string

	mu      sync.RWMutex
	bundles map[string]map[string]string // lowercase locale ("en", "de", "pt_br") -> key -> pattern
}

func newTranslationManager(lp *LuckPerms) *TranslationManager {
	base := filepath.Join(lp.DataDir(), "translations")
	tm := &TranslationManager{
		lp:          lp,
		customDir:   filepath.Join(base, "custom"),
		repoDir:     filepath.Join(base, "repository"),
		statusPath:  filepath.Join(base, "repository", "status.json"),
		metadataURL: defaultTranslationsMetadataURL,
		downloadURL: defaultTranslationsDownloadURL,
		bundles:     make(map[string]map[string]string),
	}
	tm.Reload()
	return tm
}

// SetEndpoints overrides the metadata and download endpoints (for testing).
func (tm *TranslationManager) SetEndpoints(metadataURL, downloadURL string) {
	tm.mu.Lock()
	tm.metadataURL = metadataURL
	tm.downloadURL = ensureTrailingSlash(downloadURL)
	tm.mu.Unlock()
}

// Reload reloads bundled English, repository, and custom translation files (custom overrides repository overrides bundled).
func (tm *TranslationManager) Reload() {
	_ = os.MkdirAll(tm.customDir, 0o755)
	_ = os.MkdirAll(tm.repoDir, 0o755)

	loaded := make(map[string]map[string]string)
	loaded["en"] = parseProperties(defaultTranslationsEn)

	loadDir := func(dir string) {
		entries, err := os.ReadDir(dir)
		if err != nil {
			return
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(e.Name(), ".properties") {
				continue
			}
			loc := normalizeLocaleName(strings.TrimSuffix(e.Name(), ".properties"))
			if loc == "" {
				continue
			}
			data, err := os.ReadFile(filepath.Join(dir, e.Name()))
			if err != nil {
				continue
			}
			props := parseProperties(data)
			if loaded[loc] == nil {
				loaded[loc] = props
			} else {
				for k, v := range props {
					loaded[loc][k] = v
				}
			}
		}
	}

	loadDir(tm.repoDir)
	loadDir(tm.customDir)

	tm.mu.Lock()
	tm.bundles = loaded
	tm.mu.Unlock()
}

// InstalledLocales returns sorted installed locale codes.
func (tm *TranslationManager) InstalledLocales() []string {
	tm.mu.RLock()
	out := make([]string, 0, len(tm.bundles))
	for loc := range tm.bundles {
		out = append(out, loc)
	}
	tm.mu.RUnlock()
	slices.Sort(out)
	return out
}

// Get resolves key for locale (falling back to language base, then English).
func (tm *TranslationManager) Get(locale, key string, args ...any) string {
	loc := normalizeLocaleName(locale)
	tm.mu.RLock()
	pattern, ok := tm.lookupLocked(loc, key)
	tm.mu.RUnlock()
	if !ok {
		return key
	}
	return formatJavaMessage(pattern, args...)
}

func (tm *TranslationManager) lookupLocked(loc, key string) (string, bool) {
	if loc != "" {
		if m, ok := tm.bundles[loc]; ok {
			if v, ok := m[key]; ok {
				return v, true
			}
		}
		if base, _, hasRegion := strings.Cut(loc, "_"); hasRegion {
			if m, ok := tm.bundles[base]; ok {
				if v, ok := m[key]; ok {
					return v, true
				}
			}
		}
	}
	if m, ok := tm.bundles["en"]; ok {
		if v, ok := m[key]; ok {
			return v, true
		}
	}
	return "", false
}

// AvailableLanguages queries metadata.luckperms.net for available community translations.
func (tm *TranslationManager) AvailableLanguages() ([]TranslationLanguageInfo, error) {
	tm.mu.RLock()
	metaURL := tm.metadataURL
	tm.mu.RUnlock()

	req, err := http.NewRequest(http.MethodGet, metaURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", tm.lp.Bytebin.UserAgent)
	resp, err := tm.lp.Bytebin.HTTPClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, fmt.Errorf("translations metadata HTTP %d", resp.StatusCode)
	}
	var body struct {
		CacheMaxAge int64 `json:"cacheMaxAge"`
		Languages   map[string]struct {
			Name         string `json:"name"`
			LocaleTag    string `json:"localeTag"`
			Progress     int    `json:"progress"`
			Contributors []struct {
				Name string `json:"name"`
			} `json:"contributors"`
		} `json:"languages"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, maxTranslationBundleSize)).Decode(&body); err != nil {
		return nil, err
	}
	var out []TranslationLanguageInfo
	for id, lang := range body.Languages {
		if lang.Progress <= 0 {
			continue
		}
		var contribs []string
		for _, c := range lang.Contributors {
			contribs = append(contribs, c.Name)
		}
		out = append(out, TranslationLanguageInfo{
			ID:           id,
			Name:         lang.Name,
			LocaleTag:    lang.LocaleTag,
			Progress:     lang.Progress,
			Contributors: contribs,
		})
	}
	slices.SortFunc(out, func(a, b TranslationLanguageInfo) int {
		return strings.Compare(a.LocaleTag, b.LocaleTag)
	})
	return out, nil
}

// DownloadAndInstall fetches and installs the listed translation bundles into translations/repository/.
func (tm *TranslationManager) DownloadAndInstall(langs []TranslationLanguageInfo, onProgress func(locale string)) (int, error) {
	_ = os.MkdirAll(tm.repoDir, 0o755)
	tm.mu.RLock()
	dlBase := tm.downloadURL
	tm.mu.RUnlock()

	installed := 0
	for _, lang := range langs {
		if onProgress != nil {
			onProgress(lang.LocaleTag)
		}
		req, err := http.NewRequest(http.MethodGet, dlBase+lang.ID, nil)
		if err != nil {
			continue
		}
		req.Header.Set("User-Agent", tm.lp.Bytebin.UserAgent)
		resp, err := tm.lp.Bytebin.HTTPClient.Do(req)
		if err != nil {
			continue
		}
		if resp.StatusCode >= 200 && resp.StatusCode < 300 {
			data, err := io.ReadAll(io.LimitReader(resp.Body, maxTranslationBundleSize))
			if err == nil {
				loc := normalizeLocaleName(lang.LocaleTag)
				if loc != "" {
					_ = atomicWriteFile(filepath.Join(tm.repoDir, loc+".properties"), data)
					installed++
				}
			}
		}
		resp.Body.Close()
	}
	status, _ := json.MarshalIndent(map[string]int64{"lastRefresh": time.Now().UnixMilli()}, "", "  ")
	_ = atomicWriteFile(tm.statusPath, status)
	tm.Reload()
	return installed, nil
}

func normalizeLocaleName(s string) string {
	s = strings.TrimSpace(strings.ToLower(s))
	s = strings.TrimPrefix(s, "luckperms_")
	s = strings.ReplaceAll(s, "-", "_")
	return s
}

func parseProperties(data []byte) map[string]string {
	out := make(map[string]string)
	sc := bufio.NewScanner(bytes.NewReader(data))
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") || strings.HasPrefix(line, "!") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			k, v, ok = strings.Cut(line, ":")
			if !ok {
				continue
			}
		}
		k = strings.TrimSpace(k)
		v = strings.TrimLeft(v, " \t")
		v = strings.ReplaceAll(v, `''`, `'`)
		v = strings.ReplaceAll(v, `\n`, "\n")
		out[k] = v
	}
	return out
}

func formatJavaMessage(pattern string, args ...any) string {
	if len(args) == 0 {
		return pattern
	}
	res := pattern
	for i, a := range args {
		res = strings.ReplaceAll(res, "{"+strconv.Itoa(i)+"}", fmt.Sprint(a))
	}
	return res
}
