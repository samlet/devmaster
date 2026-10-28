// devmaster-version-gate 是部署前置校验的参考客户端（it-solution 侧应复用同一语义）。
//
// 退出码：0 已发布可部署；1 版本不存在/未发布（拒绝部署）；2 调用失败（按策略处理）。
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"time"

	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"

	devv1 "github.com/xiaofeiwu/devmaster/backend/gen/go/dev/v1"
)

func main() {
	address := flag.String("address", "127.0.0.1:50091", "devmaster gRPC address")
	product := flag.String("product", "", "product code (e.g. HOSP)")
	version := flag.String("version", "", "version name (e.g. 1.0.0)")
	flag.Parse()
	if *product == "" || *version == "" {
		fmt.Fprintln(os.Stderr, "-product and -version are required")
		os.Exit(2)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	conn, err := grpc.NewClient(*address, grpc.WithTransportCredentials(insecure.NewCredentials()))
	if err != nil {
		fmt.Fprintf(os.Stderr, "connect %s: %v\n", *address, err)
		os.Exit(2)
	}
	defer conn.Close()

	response, err := devv1.NewVersionRegistryServiceClient(conn).CheckVersion(ctx, &devv1.CheckVersionRequest{
		ProductCode: *product,
		Version:     *version,
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "CheckVersion failed: %v\n", err)
		os.Exit(2)
	}
	switch {
	case !response.GetExists():
		fmt.Printf("DENY %s %s：版本不存在\n", *product, *version)
		os.Exit(1)
	case !response.GetReleased():
		fmt.Printf("DENY %s %s：状态 %s，未发布\n", *product, *version, response.GetStatus())
		os.Exit(1)
	default:
		releasedAt := ""
		if response.GetReleasedAt() != nil {
			releasedAt = response.GetReleasedAt().AsTime().Format(time.RFC3339)
		}
		fmt.Printf("ALLOW %s %s：已发布（%s）\n", *product, *version, releasedAt)
	}
}
