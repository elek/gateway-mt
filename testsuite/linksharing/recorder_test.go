package linksharing

import (
	"context"
	"fmt"
	"github.com/spacemonkeygo/monkit/v3"
	"github.com/stretchr/testify/require"
	"testing"
)

var mon = monkit.Package()

func example1(ctx context.Context) {
	defer mon.Task()(&ctx)(nil)
	fmt.Println("Do nothing")
}

func example2(ctx context.Context) (err error) {
	defer mon.Task()(&ctx)(&err)
	return fmt.Errorf("Oh no")
}

func TestMethodCallRecorder_Check(t *testing.T) {
	c := NewFuncCallCounter(FullNameEqual("storj.io/gateway-mt/testsuite/linksharing.example"))
	ctx := context.TODO()
	example1(ctx)
	_ = example2(ctx)
	err := c.Check(2)
	require.Nil(t, err)
	err = c.Check(1)
	require.NotNil(t, err)
}
