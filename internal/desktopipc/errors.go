package desktopipc

import "errors"

var (
	ErrAlreadyRunning = errors.New("Azem workspace daemon is already running")
	ErrAuthentication = errors.New("Azem IPC authentication failed")
	ErrResyncRequired = errors.New("Azem IPC client must reload a full snapshot")
)
