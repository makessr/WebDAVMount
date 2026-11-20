package providers

import (
	"encoding/json"
	"errors"
	"regexp"
	"strings"
	"time"
)

const (
	GoogleDrive  = "googledrive"
	Workspace    = "workspace"
	OneDrive     = "onedrive"
	Microsoft365 = "microsoft365"
	SharePoint   = "sharepoint"
	Yandex       = "yandex"
)

func IsOAuth(kind string) bool {
	switch kind {
	case GoogleDrive, Workspace, OneDrive, Microsoft365, SharePoint, Yandex:
		return true
	}
	return false
}

var resourceID = regexp.MustCompile(`^[A-Za-z0-9_!.-]{1,256}$`)
var googleClientID = regexp.MustCompile(`^[A-Za-z0-9_-]{1,180}\.apps\.googleusercontent\.com$`)
var googleClientSecret = regexp.MustCompile(`^[A-Za-z0-9_-]{1,256}$`)

type CloudGrant struct {
	Token        string `json:"token"`
	ClientID     string `json:"clientID,omitempty"`
	ClientSecret string `json:"clientSecret,omitempty"`
}

func (g CloudGrant) Encode() string {
	value, _ := json.Marshal(g)
	return string(value)
}

func DecodeGrant(value string) (CloudGrant, bool) {
	var grant CloudGrant
	valid := len(value) <= 65536 && json.Unmarshal([]byte(value), &grant) == nil && ValidOAuthToken(grant.Token)
	return grant, valid
}

func IsGoogle(kind string) bool {
	return kind == GoogleDrive || kind == Workspace
}

func ValidGoogleClient(id, secret string) bool {
	return googleClientID.MatchString(id) && googleClientSecret.MatchString(secret)
}

func OAuthConfig(kind, address, resource, credential string) (string, map[string]string, error) {
	var grant CloudGrant
	valid := true
	if credential != "" {
		grant, valid = DecodeGrant(credential)
	}
	token := grant.Token
	if address != "" || !valid || (!IsGoogle(kind) && (grant.ClientID != "" || grant.ClientSecret != "")) {
		return "", nil, errors.New("网盘授权信息无效，请重新授权")
	}
	params := map[string]string{}
	if token != "" {
		params["token"] = token
	}
	switch kind {
	case GoogleDrive, Workspace:
		params["scope"] = "drive.readonly"
		if token != "" {
			if !ValidGoogleClient(grant.ClientID, grant.ClientSecret) {
				return "", nil, errors.New("Google 网盘需要有效的桌面应用授权")
			}
			params["client_id"], params["client_secret"] = grant.ClientID, grant.ClientSecret
		}
		if kind == Workspace && resource != "" && resourceID.MatchString(resource) {
			params["team_drive"] = resource
		} else if resource != "" || (token != "" && kind == Workspace) {
			return "", nil, errors.New("请选择有效的 Google 共享云端硬盘")
		}
		return "drive", params, nil
	case OneDrive, Microsoft365, SharePoint:
		params["access_scopes"] = "Files.Read Files.Read.All offline_access"
		if kind == SharePoint {
			params["access_scopes"] += " Sites.Read.All"
		} else {
			params["disable_site_permission"] = "true"
		}
		if resource != "" || token != "" {
			driveType, driveID, found := strings.Cut(resource, ":")
			if !found || !resourceID.MatchString(driveID) || !allowedDriveType(kind, driveType) {
				return "", nil, errors.New("所选 Microsoft 驱动器类型不符，请重新授权选盘")
			}
			params["drive_type"], params["drive_id"] = driveType, driveID
		}
		return "onedrive", params, nil
	case Yandex:
		if resource != "" {
			return "", nil, errors.New("Yandex Disk 不需要指定驱动器")
		}
		return "yandex", params, nil
	default:
		return "", nil, errors.New("未知的浏览器授权类型")
	}
}

func allowedDriveType(kind, driveType string) bool {
	switch kind {
	case Microsoft365:
		return driveType == "business"
	case SharePoint:
		return driveType == "documentLibrary"
	case OneDrive:
		return driveType == "personal" || driveType == "business"
	}
	return false
}

func ValidOAuthToken(value string) bool {
	if len(value) == 0 || len(value) > 32768 || strings.ContainsAny(value, "\r\n") {
		return false
	}
	var token struct {
		Access  string    `json:"access_token"`
		Refresh string    `json:"refresh_token"`
		Expiry  time.Time `json:"expiry"`
	}
	return json.Unmarshal([]byte(value), &token) == nil && token.Access != "" && token.Refresh != ""
}
