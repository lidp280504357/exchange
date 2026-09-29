import { useMutation, useQueryClient } from "@tanstack/react-query";
import { useState, type FormEvent } from "react";
import { useNavigate } from "react-router";
import { api, data, describe } from "../api/client";
import { Button, Card, ErrorText, Field } from "../ui";

export function LoginPage() {
  const qc = useQueryClient();
  const navigate = useNavigate();
  const [email, setEmail] = useState("");
  const [password, setPassword] = useState("");
  const [code, setCode] = useState("");
  const login = useMutation({
    mutationFn: async () => data(await api.POST("/admin/v1/login", { body: { email, password, totp_code: code } })),
    onSuccess: (res) => {
      qc.setQueryData(["me"], res.admin);
      navigate("/", { replace: true });
    },
    onError: () => setCode(""),
  });
  const submit = (e: FormEvent) => {
    e.preventDefault();
    login.mutate();
  };
  return (
    <div className="flex min-h-screen items-center justify-center p-4">
      <div className="w-full max-w-sm">
        <Card title="Exchange 管理后台">
          <form className="space-y-3" onSubmit={submit}>
            <Field label="邮箱" name="email" type="email" autoComplete="username" required value={email} onChange={(e) => setEmail(e.target.value)} />
            <Field
              label="密码"
              name="password"
              type="password"
              autoComplete="current-password"
              required
              value={password}
              onChange={(e) => setPassword(e.target.value)}
            />
            <Field
              label="身份验证器 6 位验证码"
              name="totp_code"
              inputMode="numeric"
              autoComplete="one-time-code"
              pattern="[0-9]{6}"
              maxLength={6}
              required
              value={code}
              onChange={(e) => setCode(e.target.value.replace(/\D/g, ""))}
            />
            <ErrorText text={login.isError ? describe(login.error) : undefined} />
            <Button type="submit" className="w-full" disabled={login.isPending}>
              {login.isPending ? "登录中…" : "登录"}
            </Button>
            <p className="text-xs text-slate-500">账号由运维用 exchangectl admin create 创建；连续 5 次失败锁定 15 分钟。</p>
          </form>
        </Card>
      </div>
    </div>
  );
}
