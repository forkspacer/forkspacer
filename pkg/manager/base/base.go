package base

import (
	"context"
	"encoding/json"

	"github.com/go-logr/logr"
	"github.com/go-viper/mapstructure/v2"
	"k8s.io/client-go/rest"
)

type NewCustomManagerT func(
	ctx context.Context,
	logger logr.Logger,
	kubernetesConfig *rest.Config,
	config map[string]any,
) (IManager, error)

type MetaData map[string]any

func (metaData MetaData) String() string {
	return string(metaData.JSON())
}

func (metaData MetaData) JSON() []byte {
	data, _ := json.Marshal(metaData)
	return data
}

func (metaData *MetaData) Parse(rawData []byte) error {
	return json.Unmarshal(rawData, &metaData)
}

func (metaData MetaData) DecodeToString(key string) string {
	strData, ok := metaData[key]
	if !ok {
		return ""
	}

	var parsedStrData string
	if err := mapstructure.Decode(strData, &parsedStrData); err != nil {
		return ""
	}

	return parsedStrData
}

type IManager interface {
	Install(ctx context.Context, metaData MetaData) error
	Uninstall(ctx context.Context, metaData MetaData) error
	Sleep(ctx context.Context, metaData MetaData) error
	Resume(ctx context.Context, metaData MetaData) error
}
