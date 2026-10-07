package gsa

import (
	"bytes"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"howett.net/plist"
	"net/http"
	"strings"
)

var circleServiceURL = "https://gsa.apple.com/grandslam/GsService2/circle"

type CircleReply struct {
	Transaction string
	IDMSData    string
	Step        int
	PAKE        string
	PushToken   []byte
	ErrorCode   *int
}

// RespondToCircle uses the authenticated account's heartbeat token. Neither
// caller-supplied URLs nor notifications can select the authentication account.
func (c *Client) RespondToCircle(payload map[string]any, reply CircleReply) error {
	return c.sendCircle(payload, reply, false)
}

// InitiateCircle sends the requesting side using the current verified SRP
// challenge's IDMS token. Tokens are taken from memory, never from a notice.
func (c *Client) InitiateCircle(reply CircleReply) error {
	return c.sendCircle(c.loginPayload, reply, true)
}

func (c *Client) sendCircle(payload map[string]any, reply CircleReply, requester bool) error {
	if reply.Transaction == "" || len(reply.Transaction) > 512 || (reply.IDMSData == "" && !(requester && reply.Step == 0)) || len(reply.IDMSData) > 16384 || len(reply.PushToken) != 32 || len(reply.PAKE) > 16384 {
		return errors.New("gsa: invalid Circle response")
	}
	if (!requester && reply.Step != 1 && reply.Step != 3 && reply.Step != 5) || (requester && reply.Step != 0 && reply.Step != 2 && reply.Step != 4) {
		return errors.New("gsa: invalid Circle response step")
	}
	token := payloadToken(payload, "com.apple.gs.idms.hb")
	if requester {
		token, _ = payload["GsIdmsToken"].(string)
	}
	if token == "" {
		if requester {
			return errors.New("gsa: Circle initiation requires a password-verified IDMS token")
		}
		return errors.New("gsa: Circle response requires heartbeat authentication")
	}
	headers, err := c.authenticatedDeviceHeaders(payload)
	if err != nil {
		return err
	}
	adsid := payload["adsid"].(string)
	if requester {
		headers["X-Apple-Identity-Token"] = base64.StdEncoding.EncodeToString([]byte(adsid + ":" + token))
	} else {
		headers["X-Apple-HB-Token"] = base64.StdEncoding.EncodeToString([]byte(adsid + ":" + token))
	}
	headers["X-Apple-I-UrlSwitch-Info"] = base64.StdEncoding.EncodeToString([]byte(adsid + ":circle"))
	headers["X-Apple-I-Device-Configuration-Mode"] = "0"
	headers["X-Apple-I-DeviceUserMode"] = "0"
	headers["X-Apple-I-TimeZone-Offset"] = "0"
	request := map[string]any{"atxid": reply.Transaction, "circlestep": reply.Step, "idmsdata": reply.IDMSData, "pakedata": reply.PAKE, "ptkn": strings.ToUpper(hex.EncodeToString(reply.PushToken))}
	if requester && reply.Step == 0 {
		delete(request, "idmsdata")
	}
	if reply.ErrorCode != nil {
		request["ec"] = *reply.ErrorCode
	}
	body, err := plist.Marshal(map[string]any{"Header": map[string]any{}, "Request": request}, plist.XMLFormat)
	if err != nil {
		return err
	}
	data, err := c.accountRequest(http.MethodPost, circleServiceURL, headers, body)
	if err != nil {
		return err
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return errors.New("gsa: empty Circle response")
	}
	var response map[string]any
	if _, err = plist.Unmarshal(data, &response); err != nil {
		return errors.New("gsa: invalid Circle response plist")
	}
	if inner, ok := response["Response"].(map[string]any); ok {
		response = inner
	}
	if _, ok := response["Status"]; ok {
		_, err = checkStatus(response, "circle", "")
		return err
	}
	if ec, ok := asInt64(response["ec"]); ok && ec != 0 {
		return &AuthenticationError{Operation: "circle", Code: ec}
	}
	// CircleResponse can be an empty dictionary or contain only an optional sid.
	// A successful transport acknowledgement does not prove verification success.
	return nil
}
