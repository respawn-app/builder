package oauthadapter

import (
	serverauth "builder/server/auth"
	sharedauth "builder/shared/auth"
)

type OpenAIOAuthOptions = serverauth.OpenAIOAuthOptions
type DeviceCode = serverauth.DeviceCode
type BrowserCallback = serverauth.BrowserCallback
type BrowserAuthSession = serverauth.BrowserAuthSession
type DeviceAuthorizationGrant = serverauth.DeviceAuthorizationGrant
type Method = sharedauth.Method
