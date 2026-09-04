package app

import (
	"context"
	"errors"
)

type fakeSteamPriceSource struct {
	prices map[string]string
	err    error
	calls  []string
}

func (source *fakeSteamPriceSource) FetchLowestPrice(
	_ context.Context,
	marketHashName string,
) (string, error) {
	source.calls = append(source.calls, marketHashName)
	if source.err != nil {
		return "", source.err
	}
	price, exists := source.prices[marketHashName]
	if !exists {
		return "", errors.New("missing fake Steam price")
	}
	return price, nil
}
