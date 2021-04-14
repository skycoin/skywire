package logstore

import "github.com/sirupsen/logrus"

type Store interface {
	GetLogs() string
	GetHook() logrus.Hook
}

func MakeStore(maxEntries int) Store {
	panic("not implemented")
}
