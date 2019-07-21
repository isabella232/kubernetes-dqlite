package factory

import (
	restful "github.com/emicklei/go-restful"
)

type Rest struct{}

func (r Rest) Install(c *restful.Container) {
}
