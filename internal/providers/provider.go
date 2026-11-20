// Package providers defines the connection types accepted by the desktop
// client and its portable backup reader.
package providers

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

const (
	WebDAV    = "webdav"
	Nextcloud = "nextcloud"
	MEGA      = "mega"
	FTP       = "ftp"
	SFTP      = "sftp"
	MinIO     = "minio"
	Tencent   = "tencent"
	R2        = "r2"
	AWS       = "aws"
	Alibaba   = "alibaba"
	Azure     = "azure"
	B2        = "backblaze"
)

var bucketName = regexp.MustCompile(`^[a-z0-9][a-z0-9.-]{1,61}[a-z0-9]$`)
var cosHost = regexp.MustCompile(`^cos\.([a-z0-9-]+)\.myqcloud\.com$`)
var r2Host = regexp.MustCompile(`^[a-f0-9]{32}(?:\.eu|\.fedramp)?\.r2\.cloudflarestorage\.com$`)
var awsHost = regexp.MustCompile(`^s3\.([a-z0-9-]+)\.amazonaws\.com(\.cn)?$`)
var ossHost = regexp.MustCompile(`^oss-([a-z0-9-]+)\.aliyuncs\.com$`)
var azureHost = regexp.MustCompile(`^([a-z0-9]{3,24})\.blob\.core\.windows\.net$`)
var azureContainer = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{1,61}[a-z0-9]$`)
var b2Bucket = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9-]{4,61}[a-zA-Z0-9]$`)

func IsObjectStorage(kind string) bool {
	switch kind {
	case MinIO, Tencent, R2, AWS, Alibaba, Azure, B2:
		return true
	}
	return false
}

func RemoteRoot(kind, address string) string {
	if !IsObjectStorage(kind) {
		return ""
	}
	u, err := url.Parse(address)
	if err != nil {
		return ""
	}
	return strings.Trim(u.Path, "/")
}

func Kind(value string) string {
	if value == "" {
		return WebDAV
	}
	return value
}

// EngineConfig maps a typed profile to a fixed set of rclone options.
// A missing password is allowed so users can restore backups without secrets.
func EngineConfig(provider, address, user, password, hostKey string) (string, map[string]string, error) {
	kind := Kind(provider)
	if hostKey != "" && kind != SFTP {
		return "", nil, errors.New("服务器公钥只用于 SFTP 连接")
	}
	if IsOAuth(kind) {
		return OAuthConfig(kind, address, user, password)
	}
	if kind == MEGA {
		if address != "" || strings.TrimSpace(user) == "" {
			return "", nil, errors.New("MEGA 需要账号邮箱，无需填写服务器地址")
		}
		return "mega", map[string]string{"user": user, "pass": password}, nil
	}
	if kind != WebDAV && kind != Nextcloud && kind != FTP && kind != SFTP && !IsObjectStorage(kind) {
		return "", nil, errors.New("不支持此连接类型")
	}
	u, err := url.Parse(address)
	if err != nil || u.Hostname() == "" || u.User != nil || u.ForceQuery || u.RawQuery != "" ||
		u.Fragment != "" || strings.Contains(address, "#") || strings.ContainsFunc(u.Hostname(), unicode.IsSpace) ||
		strings.ContainsFunc(u.Hostname(), unicode.IsControl) {
		return "", nil, errors.New("请输入不含账号、密码、查询参数或片段的服务器地址")
	}
	if kind == FTP {
		if u.Path != "" && u.Path != "/" {
			return "", nil, errors.New("FTP 地址请填写服务器根地址，不包含目录")
		}
		port := u.Port()
		if port != "" {
			n, err := strconv.ParseUint(port, 10, 16)
			if err != nil || n == 0 {
				return "", nil, errors.New("FTP 端口须为 1 至 65535")
			}
		}
		params := map[string]string{"host": u.Hostname(), "user": user, "pass": password}
		switch u.Scheme {
		case "ftp", "ftpes":
			if port == "" {
				port = "21"
			}
			if u.Scheme == "ftpes" {
				params["explicit_tls"] = "true"
			}
		case "ftps":
			if port == "" {
				port = "990"
			}
			params["tls"] = "true"
		default:
			return "", nil, errors.New("请选择 ftp://、ftpes:// 或 ftps:// 地址")
		}
		params["port"] = port
		return "ftp", params, nil
	}
	if kind == SFTP {
		if u.Scheme != "sftp" || (u.Path != "" && u.Path != "/") || strings.TrimSpace(user) == "" {
			return "", nil, errors.New("请填写 SFTP 服务器根地址和用户名")
		}
		port := u.Port()
		if port == "" {
			port = "22"
		}
		if n, err := strconv.ParseUint(port, 10, 16); err != nil || n == 0 {
			return "", nil, errors.New("SFTP 端口须为 1 至 65535")
		}
		key, err := canonicalHostKey(hostKey)
		if err != nil {
			return "", nil, err
		}
		return "sftp", map[string]string{
			"host": u.Hostname(), "port": port, "user": user, "pass": password,
			"host_keys": key, "pin_host_key": "false", "shell_type": "none",
			"disable_hashcheck": "true", "key_use_agent": "false",
		}, nil
	}
	loopback := u.Hostname() == "localhost" || u.Hostname() == "127.0.0.1" || u.Hostname() == "::1"
	if u.Scheme != "https" && !(u.Scheme == "http" && loopback) {
		return "", nil, errors.New("远程连接必须使用 HTTPS；HTTP 仅允许本机测试")
	}
	if IsObjectStorage(kind) {
		bucket := strings.TrimPrefix(u.Path, "/")
		validBucket := bucketName.MatchString(bucket) && !strings.Contains(bucket, "..")
		if kind == Azure {
			validBucket = azureContainer.MatchString(bucket) && !strings.Contains(bucket, "--")
		}
		if kind == B2 {
			validBucket = b2Bucket.MatchString(bucket)
		}
		if !validBucket || u.RawPath != "" {
			return "", nil, errors.New("请在服务器地址末尾填入一个桶名，例如 https://storage.example.com/my-bucket")
		}
		if strings.TrimSpace(user) == "" {
			return "", nil, errors.New("请填写对象存储的访问密钥 ID")
		}
		params := map[string]string{
			"endpoint": u.Scheme + "://" + u.Host, "env_auth": "false",
			"access_key_id": user, "secret_access_key": password,
			"provider": "Minio", "region": "us-east-1", "force_path_style": "true", "no_check_bucket": "true",
		}
		officialHTTPS := u.Scheme == "https" && (u.Port() == "" || u.Port() == "443")
		switch kind {
		case AWS:
			match := awsHost.FindStringSubmatch(u.Hostname())
			if match == nil || !officialHTTPS {
				return "", nil, errors.New("请填写 AWS S3 区域端点和桶名，例如 https://s3.us-east-1.amazonaws.com/my-bucket")
			}
			params["provider"], params["region"] = "AWS", match[1]
			params["force_path_style"] = "false"
		case Alibaba:
			match := ossHost.FindStringSubmatch(u.Hostname())
			if match == nil || !officialHTTPS {
				return "", nil, errors.New("请填写阿里云 OSS 公网地域端点和桶名")
			}
			params["provider"], params["region"] = "Alibaba", match[1]
			params["force_path_style"] = "false"
		case Azure:
			match := azureHost.FindStringSubmatch(u.Hostname())
			if match == nil || !officialHTTPS || user != match[1] {
				return "", nil, errors.New("请填写 Azure Blob 公有云容器地址，存储账户名需与地址一致")
			}
			return "azureblob", map[string]string{"account": user, "key": password, "endpoint": "https://" + u.Hostname(), "env_auth": "false"}, nil
		case B2:
			if !officialHTTPS || u.Hostname() != "api.backblazeb2.com" {
				return "", nil, errors.New("请填写 https://api.backblazeb2.com/桶名")
			}
			return "b2", map[string]string{"account": user, "key": password}, nil
		case Tencent:
			match := cosHost.FindStringSubmatch(u.Hostname())
			if match == nil || u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
				return "", nil, errors.New("请使用腾讯云 COS 地域端点，例如 https://cos.ap-guangzhou.myqcloud.com/桶名-APPID")
			}
			params["provider"], params["region"] = "TencentCOS", match[1]
			params["force_path_style"] = "false"
		case R2:
			if !r2Host.MatchString(u.Hostname()) || u.Scheme != "https" || (u.Port() != "" && u.Port() != "443") {
				return "", nil, errors.New("请使用 Cloudflare R2 的账户 S3 API 端点及桶名")
			}
			params["provider"], params["region"] = "Cloudflare", "auto"
		}
		return "s3", params, nil
	}
	params := map[string]string{"url": address, "user": user, "pass": password, "vendor": "other"}
	if kind == Nextcloud {
		params["vendor"] = "nextcloud"
		// The read-only client should not derive Nextcloud's upload URL.
		params["nextcloud_chunk_size"] = "0"
	}
	return "webdav", params, nil
}

func canonicalHostKey(value string) (string, error) {
	invalid := errors.New("请填写经核验的 SSH 服务器公钥，格式为「算法 Base64公钥」")
	if len(value) > 8192 || strings.ContainsAny(value, ",\r\n") {
		return "", invalid
	}
	fields := strings.Fields(value)
	if len(fields) != 2 || strings.Contains(fields[0], "-cert-") {
		return "", invalid
	}
	data, err := base64.StdEncoding.DecodeString(fields[1])
	if err != nil || len(data) < 5 {
		return "", invalid
	}
	length := int(binary.BigEndian.Uint32(data[:4]))
	if length != len(fields[0]) || length >= len(data)-4 || string(data[4:4+length]) != fields[0] {
		return "", invalid
	}
	return fields[0] + " " + fields[1], nil
}
