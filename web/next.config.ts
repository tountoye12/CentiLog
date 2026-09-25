import type { NextConfig } from "next";

const nextConfig: NextConfig = {
  output: "standalone",
  async rewrites() {
    return [
      {
        source: "/api/:path*",
        destination: `${process.env.CENTILOG_API_ORIGIN || "http://localhost:8081"}/api/:path*`,
      },
    ];
  },
};

export default nextConfig;
