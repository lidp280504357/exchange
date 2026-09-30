import { routes } from "@exchange/core";
import { lazyPage, type PageRoute } from "../../routing";

// Sign-in, sign-up and password reset, full screen (design §7.2).
const Login = lazyPage(() => import("./Login"), () => import("../../i18n/auth"));
const Register = lazyPage(() => import("./Register"), () => import("../../i18n/auth"));
const Reset = lazyPage(() => import("./Reset"), () => import("../../i18n/auth"));

export const authRoutes: PageRoute[] = [
  { path: routes.login, element: <Login />, shell: "auth" },
  { path: routes.register, element: <Register />, shell: "auth" },
  { path: routes.reset, element: <Reset />, shell: "auth" },
];
