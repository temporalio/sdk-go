//go:build !linux

package hostinfo

import "errors"

func newCGroupInfo() cGroupInfo {
	return &cGroupInfoImpl{}
}

type cGroupInfoImpl struct {
}

func (p *cGroupInfoImpl) Update() (bool, error) {
	return false, errors.New("cgroup is not supported on this platform")
}

func (p *cGroupInfoImpl) GetLastMemUsage() float64 {
	return 0
}

func (p *cGroupInfoImpl) GetLastCPUUsage() float64 {
	return 0
}
