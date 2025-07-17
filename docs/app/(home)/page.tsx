import Link from 'next/link';
import { ChevronRight, Code2, Zap, Shield, Github, ExternalLink } from 'lucide-react';

// 使用 fumadocs-ui 提供的 shadcn 组件
type ButtonVariant = 'default' | 'secondary' | 'outline' | 'ghost';
type ButtonSize = 'default' | 'sm' | 'lg';

interface ButtonProps {
  children: React.ReactNode;
  className?: string;
  variant?: ButtonVariant;
  size?: ButtonSize;
  asChild?: boolean;
  [key: string]: any;
}

const Button = ({ children, className = '', variant = 'default', size = 'default', asChild, ...props }: ButtonProps) => {
  const baseClasses = "inline-flex items-center justify-center whitespace-nowrap rounded-md text-sm font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-offset-2 disabled:opacity-50 disabled:pointer-events-none ring-offset-background";

  const variants: Record<ButtonVariant, string> = {
    default: "bg-primary text-primary-foreground hover:bg-primary/90",
    secondary: "bg-secondary text-secondary-foreground hover:bg-secondary/80",
    outline: "border border-input hover:bg-accent hover:text-accent-foreground",
    ghost: "hover:bg-accent hover:text-accent-foreground",
  };

  const sizes: Record<ButtonSize, string> = {
    default: "h-10 py-2 px-4",
    sm: "h-9 px-3",
    lg: "h-11 px-8",
  };

  const classes = `${baseClasses} ${variants[variant]} ${sizes[size]} ${className}`;

  if (asChild) {
    return <div className={classes} {...props}>{children}</div>;
  }

  return <button className={classes} {...props}>{children}</button>;
};

const Card = ({ children, className = '', ...props }: any) => (
  <div className={`rounded-lg border bg-card text-card-foreground shadow-sm ${className}`} {...props}>
    {children}
  </div>
);

const CardHeader = ({ children, className = '', ...props }: any) => (
  <div className={`flex flex-col space-y-1.5 p-6 ${className}`} {...props}>
    {children}
  </div>
);

const CardContent = ({ children, className = '', ...props }: any) => (
  <div className={`p-6 pt-0 ${className}`} {...props}>
    {children}
  </div>
);

const CardTitle = ({ children, className = '', ...props }: any) => (
  <h3 className={`text-2xl font-semibold leading-none tracking-tight ${className}`} {...props}>
    {children}
  </h3>
);

const CardDescription = ({ children, className = '', ...props }: any) => (
  <p className={`text-sm text-muted-foreground ${className}`} {...props}>
    {children}
  </p>
);

type BadgeVariant = 'default' | 'secondary' | 'outline';

interface BadgeProps {
  children: React.ReactNode;
  className?: string;
  variant?: BadgeVariant;
  [key: string]: any;
}

const Badge = ({ children, className = '', variant = 'default', ...props }: BadgeProps) => {
  const variants: Record<BadgeVariant, string> = {
    default: "bg-primary text-primary-foreground hover:bg-primary/80",
    secondary: "bg-secondary text-secondary-foreground hover:bg-secondary/80",
    outline: "text-foreground border border-input",
  };

  return (
    <div className={`inline-flex items-center rounded-full px-2.5 py-0.5 text-xs font-semibold transition-colors focus:outline-none focus:ring-2 focus:ring-ring focus:ring-offset-2 ${variants[variant]} ${className}`} {...props}>
      {children}
    </div>
  );
};

export default function HomePage() {
  return (
    <div className="min-h-screen bg-background">
      {/* Hero Section */}
      <section className="relative flex items-center justify-center min-h-screen px-6">
        {/* 背景装饰 */}
        <div className="absolute inset-0 overflow-hidden">
          <div className="absolute -top-40 -right-40 w-80 h-80 hero-gradient rounded-full opacity-20 blur-3xl"></div>
          <div className="absolute -bottom-40 -left-40 w-96 h-96 hero-gradient rounded-full opacity-15 blur-3xl"></div>
        </div>

        <div className="relative z-10 text-center max-w-5xl mx-auto">
          {/* Logo */}
          <div className="mb-8 flex justify-center">
            <div className="w-20 h-20 hero-gradient rounded-2xl flex items-center justify-center shadow-lg">
              <svg
                width="48"
                height="48"
                viewBox="0 0 24 24"
                fill="none"
                xmlns="http://www.w3.org/2000/svg"
                className="text-white"
              >
                <rect
                  x="2"
                  y="8"
                  width="20"
                  height="8"
                  rx="4"
                  fill="currentColor"
                  opacity="0.3"
                />
                <circle cx="7" cy="12" r="2" fill="currentColor" />
                <circle cx="17" cy="12" r="2" fill="currentColor" />
                <path
                  d="M10 12h4"
                  stroke="currentColor"
                  strokeWidth="2"
                  strokeLinecap="round"
                />
              </svg>
            </div>
          </div>

          {/* 标题和徽章 */}
          <div className="mb-6">
            <Badge className="mb-4">
              🚀 Go Web 开发工具库
            </Badge>
            <h1 className="text-5xl md:text-7xl font-bold mb-6 tracking-tight">
              <span className="bg-gradient-to-r from-primary to-chart-3 bg-clip-text text-transparent">
                go-web-utils
              </span>
            </h1>
          </div>

          {/* 副标题 */}
          <p className="text-xl md:text-2xl text-muted-foreground mb-12 max-w-3xl mx-auto leading-relaxed">
            一个优雅、高效的 Go Web 项目实用工具库
            <br />
            让您的开发更加简单、快速、可靠
          </p>

          {/* 操作按钮 */}
          <div className="flex flex-col sm:flex-row gap-4 justify-center items-center mb-16">
            <Button size="lg" asChild>
              <Link href="/docs" className="flex items-center gap-2">
                查看文档
                <ChevronRight className="w-4 h-4" />
              </Link>
            </Button>

            <Button variant="outline" size="lg" asChild>
              <a
                href="https://github.com/woodchen-ink/go-web-utils"
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-2"
              >
                <Github className="w-4 h-4" />
                GitHub
              </a>
            </Button>

            <Button variant="ghost" size="lg" asChild>
              <a
                href="https://pkg.go.dev/github.com/woodchen-ink/go-web-utils"
                target="_blank"
                rel="noopener noreferrer"
                className="flex items-center gap-2"
              >
                pkg.go.dev
                <ExternalLink className="w-4 h-4" />
              </a>
            </Button>
          </div>

          {/* 代码示例 */}
          <Card className="code-block mb-12 text-left max-w-3xl mx-auto">
            <CardHeader>
              <div className="flex items-center justify-between">
                <CardTitle className="text-base">快速开始</CardTitle>
                <div className="flex gap-2">
                  <div className="w-3 h-3 bg-destructive rounded-full"></div>
                  <div className="w-3 h-3 bg-chart-4 rounded-full"></div>
                  <div className="w-3 h-3 bg-chart-3 rounded-full"></div>
                </div>
              </div>
            </CardHeader>
            <CardContent>
              <pre className="text-sm overflow-x-auto">
                <code className="text-foreground">
{`go get github.com/woodchen-ink/go-web-utils`}
                </code>
              </pre>
            </CardContent>
          </Card>

          {/* 统计信息 */}
          <div className="grid grid-cols-1 md:grid-cols-3 gap-6 mb-16">
            <div className="text-center">
              <div className="text-2xl font-bold text-primary mb-2">零依赖</div>
              <div className="text-sm text-muted-foreground">基于 Go 标准库</div>
            </div>
            <div className="text-center">
              <div className="text-2xl font-bold text-primary mb-2">轻量级</div>
              <div className="text-sm text-muted-foreground">高性能设计</div>
            </div>
            <div className="text-center">
              <div className="text-2xl font-bold text-primary mb-2">MIT</div>
              <div className="text-sm text-muted-foreground">开源许可证</div>
            </div>
          </div>

          {/* 底部信息 */}
          <div className="text-center">
            <p className="text-sm text-muted-foreground">
              MIT License •
              <a href="https://github.com/woodchen-ink" className="text-primary hover:underline ml-1">
                @woodchen-ink
              </a>
            </p>
          </div>
        </div>
      </section>
    </div>
  );
}
