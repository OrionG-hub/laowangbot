package webdav

import (
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"
)

func numberedName(name string, number int) string {
	if number == 0 {
		return name
	}
	ext := path.Ext(name)
	return fmt.Sprintf("%s (%d)%s", strings.TrimSuffix(name, ext), number, ext)
}

func sameNameSeries(name, original string) bool {
	if name == original {
		return true
	}
	ext := path.Ext(original)
	prefix := strings.TrimSuffix(original, ext) + " ("
	suffix := ")" + ext
	if !strings.HasPrefix(name, prefix) || !strings.HasSuffix(name, suffix) {
		return false
	}
	number := strings.TrimSuffix(strings.TrimPrefix(name, prefix), suffix)
	n, err := strconv.Atoi(number)
	return err == nil && n > 0 && strconv.Itoa(n) == number
}

// Read the whole directory so gaps in numbering do not hide duplicates in
// later numbered files. Final MOVE still forbids overwrite if another writer
// takes the selected name while this upload is in progress.
func (d *davClient) destination(ctx context.Context, directory, original string, size int64, digest []byte, target string, records []Record) (string, error) {
	res, err := d.request(ctx, "PROPFIND", directory+"/", map[string]string{"Depth": "1"}, "", 0)
	if err != nil {
		return "", err
	}
	if res.status != 207 {
		return "", fmt.Errorf("同名文件检查失败：HTTP %d；未上传", res.status)
	}
	var listing struct {
		XMLName   xml.Name `xml:"DAV: multistatus"`
		Responses []struct {
			Href string `xml:"DAV: href"`
		} `xml:"DAV: response"`
	}
	if err := xml.Unmarshal(res.body, &listing); err != nil {
		return "", errors.New("同名文件检查失败：目录响应无效；未上传")
	}
	base, err := url.Parse(d.url(directory + "/"))
	if err != nil {
		return "", errors.New("同名文件检查地址无效")
	}
	occupied := map[string]bool{}
	for _, item := range listing.Responses {
		href, err := url.Parse(item.Href)
		if err != nil || item.Href == "" {
			return "", errors.New("同名文件检查失败：目录路径无效；未上传")
		}
		entry := base.ResolveReference(href)
		if entry.Scheme != base.Scheme || entry.Host != base.Host {
			return "", errors.New("同名文件检查失败：目录地址不一致；未上传")
		}
		itemPath := strings.TrimSuffix(entry.Path, "/")
		if path.Dir(itemPath) == strings.TrimSuffix(base.Path, "/") {
			occupied[path.Base(itemPath)] = true
		}
	}
	candidates := map[string]bool{}
	for name := range occupied {
		if sameNameSeries(name, original) {
			candidates[name] = true
		}
	}
	// Old prefixed uploads retain their paths. Compare their current remote
	// contents when the record identifies the same original name and directory.
	for _, row := range records {
		if row.Target == target && row.Filename == original && path.Dir(row.RemotePath) == directory && occupied[path.Base(row.RemotePath)] {
			candidates[path.Base(row.RemotePath)] = true
		}
	}
	names := make([]string, 0, len(candidates))
	for name := range candidates {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		matches, err := d.contentMatches(ctx, directory+"/"+name, size, digest)
		if err != nil {
			return "", err
		}
		if matches {
			return "", fmt.Errorf("同名文件且 SHA256 相同，拒绝重复上传：%s", name)
		}
	}
	for number := 0; ; number++ {
		name := numberedName(original, number)
		if !occupied[name] {
			return directory + "/" + name, nil
		}
	}
}
