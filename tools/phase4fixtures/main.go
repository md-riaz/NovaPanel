// Command phase4fixtures emits nonsecret renderer fixtures for confined labs only.
package main

import (
	"encoding/json"
	"os"

	"novapanel/internal/database"
	"novapanel/internal/render/php"
)

func main() {
	pool, err := php.Render(php.Desired{Name: "lab-site", Type: "php", Version: "8.3", UID: 33, GID: 33})
	if err != nil {
		panic(err)
	}
	grants, err := database.RenderGrants("d_01arz3ndektsv4rrffq69g5fav", "u_01arz3ndektsv4rrffq69g5faw")
	if err != nil {
		panic(err)
	}
	if err := json.NewEncoder(os.Stdout).Encode(struct {
		Pool   string
		Grants database.Grants
	}{string(pool), grants}); err != nil {
		panic(err)
	}
}
