import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  // 控制台只读面直连 Go 的 HTTP 网关；写操作走 gRPC，M1 再接 BFF 签名。
  async rewrites() {
    return [
      {
        source: "/api/backend/:path*",
        destination: `${process.env.DEVMASTER_API_URL ?? "http://127.0.0.1:8291"}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
