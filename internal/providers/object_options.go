package providers

import (
	"errors"
	"net/url"
	"regexp"
	"strings"
)

type ObjectOptions struct {
	Mode         string `json:"mode,omitempty"`
	Region       string `json:"region,omitempty"`
	SessionToken string `json:"sessionToken,omitempty"`
	Tenant       string `json:"tenant,omitempty"`
	ClientID     string `json:"clientId,omitempty"`
}

var objectRegion = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,62}$`)
var azureUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// ObjectEngineConfig accepts optional explicit credentials without enabling
// ambient Azure or AWS credential discovery.
func ObjectEngineConfig(kind, address, user, password, hostKey string, options *ObjectOptions) (string, map[string]string, error) {
	if options == nil || *options == (ObjectOptions{}) {
		return EngineConfig(kind, address, user, password, hostKey)
	}
	o := *options
	switch kind {
	case AWS, MinIO:
		if o.Tenant != "" || o.ClientID != "" || o.Mode != "" && (o.Mode != "session" || kind != AWS) ||
			o.SessionToken != "" && o.Mode != "session" || len(o.SessionToken) > 16384 || strings.ContainsAny(o.SessionToken, "\x00\r\n") {
			return "", nil, errors.New("S3 的认证方式或会话令牌无效")
		}
		base := kind
		if o.Region != "" {
			if !objectRegion.MatchString(o.Region) {
				return "", nil, errors.New("请填写有效的 S3 区域，例如 us-east-1")
			}
			base = MinIO
			if u, err := url.Parse(address); err == nil && kind == AWS && awsHost.MatchString(u.Hostname()) {
				base = AWS
			}
		}
		backend, params, err := EngineConfig(base, address, user, password, hostKey)
		if err != nil {
			return "", nil, err
		}
		if o.Region != "" {
			if base == AWS && o.Region != params["region"] {
				return "", nil, errors.New("AWS 区域必须与标准端点一致")
			}
			params["region"] = o.Region
		}
		if o.Mode == "session" && o.SessionToken != "" {
			params["session_token"] = o.SessionToken
		}
		return backend, params, nil
	case Azure:
		if o.Region != "" || o.SessionToken != "" {
			return "", nil, errors.New("Azure 连接的认证字段无效")
		}
		backend, params, err := EngineConfig(kind, address, user, "", hostKey)
		if err != nil {
			return "", nil, err
		}
		switch o.Mode {
		case "sas":
			if o.Tenant != "" || o.ClientID != "" {
				break
			}
			if password != "" {
				sasURL, err := azureContainerSAS(address, password)
				if err != nil {
					return "", nil, err
				}
				params["sas_url"] = sasURL
				delete(params, "account")
			}
			return backend, params, nil
		case "entra":
			if azureUUID.MatchString(o.Tenant) && azureUUID.MatchString(o.ClientID) {
				params["tenant"], params["client_id"], params["client_secret"] = o.Tenant, o.ClientID, password
				return backend, params, nil
			}
		}
		return "", nil, errors.New("Azure SAS 或 Entra 配置无效，请检查认证类型、租户和应用 ID")
	default:
		return "", nil, errors.New("该网盘类型不支持对象存储高级认证")
	}
}

func azureContainerSAS(address, raw string) (string, error) {
	bad := errors.New("请提供此容器的有效 Azure SAS URL 或 SAS 查询字符串")
	base, _ := url.Parse(address) // The container endpoint has already been validated.
	value := strings.TrimSpace(raw)
	if len(value) > 8192 || strings.ContainsAny(value, "#\r\n\x00") {
		return "", bad
	}
	if strings.HasPrefix(value, "https://") {
		parsed, err := url.Parse(value)
		if err != nil || parsed.User != nil || parsed.Host != base.Host || parsed.Path != base.Path ||
			parsed.RawPath != "" || parsed.Scheme != base.Scheme {
			return "", bad
		}
		value = parsed.RawQuery
	}
	value = strings.TrimPrefix(value, "?")
	query, err := url.ParseQuery(value)
	if err != nil || len(query["sig"]) != 1 || query.Get("sig") == "" ||
		len(query["sr"]) != 1 || query.Get("sr") != "c" {
		return "", bad
	}
	base.RawQuery = value
	return base.String(), nil
}
