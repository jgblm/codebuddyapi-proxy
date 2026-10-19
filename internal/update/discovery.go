package update

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// latest 优先使用官网的 latest 重定向，不消耗共享代理出口的匿名 API 额度。
// 网页不可达时才使用 API；每条发现路径单独限时，给备用路径保留时间。
func (m *Manager) latest(ctx context.Context) (release, error) {
	if err := ctx.Err(); err != nil {
		return release{}, err
	}
	webCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	rel, webErr := m.latestWeb(webCtx)
	cancel()
	if webErr == nil {
		return rel, nil
	}
	if ctx.Err() != nil {
		return release{}, ctx.Err()
	}
	apiCtx, cancel := context.WithTimeout(ctx, 12*time.Second)
	defer cancel()
	data, err := m.get(apiCtx, latestURL, 2<<20)
	if err == nil {
		err = json.Unmarshal(data, &rel)
	}
	if err != nil {
		return release{}, fmt.Errorf("官方 Release 网页检查失败：%v；备用 API：%w", webErr, err)
	}
	return rel, nil
}

func (m *Manager) latestWeb(ctx context.Context) (release, error) {
	resp, err := m.head(ctx, latestWebURL)
	if err != nil {
		return release{}, err
	}
	// 只接受本仓库官方正式版的 tag 路径，不能把重定向提供的任意地址当下载源。
	if resp.Request == nil || resp.Request.URL == nil {
		return release{}, errors.New("GitHub 未返回正式 Release 地址")
	}
	u := resp.Request.URL
	tag, ok := strings.CutPrefix(u.String(), releaseTagBase)
	if !ok || u.RawQuery != "" || u.Fragment != "" || parseVersion(tag) == nil {
		return release{}, errors.New("GitHub 最新 Release 地址或版本无效")
	}
	base := downloadBase + url.PathEscape(tag) + "/"
	sums, err := m.get(ctx, base+"SHA256SUMS.txt", 1<<20)
	if err != nil {
		return release{}, err
	}
	if _, err := checksum(sums, m.asset); err != nil {
		return release{}, err
	}
	binary, err := m.head(ctx, base+m.asset)
	if err != nil {
		return release{}, err
	}
	if binary.ContentLength <= 0 || binary.ContentLength > maxBinary {
		return release{}, errors.New("官方 Release 缺少有效的当前架构文件大小")
	}
	return release{Tag: tag, Assets: []asset{
		{Name: m.asset, Size: binary.ContentLength},
		{Name: "SHA256SUMS.txt", Size: int64(len(sums))},
	}}, nil
}

func (m *Manager) head(ctx context.Context, address string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodHead, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "buddy-proxy-manual-updater")
	resp, err := m.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp)
	}
	return resp, nil
}

// checksum 在检查与下载阶段使用同一验证规则；正式安装仍重新读取清单并校验字节。
func checksum(sums []byte, name string) (string, error) {
	expected := ""
	found := false
	for line := range strings.SplitSeq(string(sums), "\n") {
		fields := strings.Fields(line)
		if len(fields) == 2 && strings.TrimPrefix(fields[1], "*") == name {
			if found {
				return "", errors.New("SHA256SUMS.txt 中目标条目重复")
			}
			found = true
			expected = strings.ToLower(fields[0])
		}
	}
	if decoded, err := hex.DecodeString(expected); err != nil || len(decoded) != sha256.Size {
		return "", errors.New("SHA256SUMS.txt 缺少有效目标校验值")
	}
	return expected, nil
}

func responseError(resp *http.Response) error {
	if (resp.StatusCode == http.StatusForbidden && resp.Header.Get("X-RateLimit-Remaining") == "0") ||
		resp.StatusCode == http.StatusTooManyRequests {
		message := "GitHub 请求已限流（共享代理出口可能共用额度），请稍后重试"
		if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
			message += "；额度恢复时间：" + time.Unix(reset, 0).Local().Format(time.RFC3339)
		}
		return errors.New(message)
	}
	return fmt.Errorf("GitHub 更新请求失败：HTTP %d", resp.StatusCode)
}
