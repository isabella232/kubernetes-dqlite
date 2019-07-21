package api

import (
	"fmt"
	"net/http"

	"github.com/emicklei/go-restful"
	"github.com/freeekanayaka/kvsql/server/membership"
)

func clusterHandler(membership *membership.Membership) http.Handler {
	ws := new(restful.WebService)
	ws.Path("/cluster").Consumes(restful.MIME_JSON).Produces(restful.MIME_JSON)
	ws.Doc("dqlite cluster management")
	ws.Route(ws.GET("/").To(
		func(req *restful.Request, resp *restful.Response) {
			servers, err := membership.List()
			if err != nil {
				msg := fmt.Sprintf("500 can't list servers: %v", err)
				http.Error(resp, msg, http.StatusServiceUnavailable)
				return
			}
			resp.WriteEntity(servers)
		}))

	c := restful.NewContainer()
	c.Add(ws)

	return c
}
