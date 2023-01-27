package gh

import (
	"fmt"

	"github.com/go-git/go-git/v5/plumbing"

	"github.com/go-git/go-git/v5"
)

func (o GitOptions) Release(dir string) error {

	r, err := git.PlainOpen(dir)
	if err != nil {
		return err
	}

	tagrefs, err := r.Tags()
	if err != nil {
		return err
	}

	err = tagrefs.ForEach(func(t *plumbing.Reference) error {
		fmt.Println(t)
		return nil
	})

	return err

}
