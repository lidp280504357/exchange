import { ApiError, errorText } from "@exchange/core";
import { zodResolver } from "@hookform/resolvers/zod";
import { CircleAlert } from "lucide-react";
import {
  cloneElement,
  isValidElement,
  useId,
  type FormHTMLAttributes,
  type ReactElement,
  type ReactNode,
} from "react";
import {
  FormProvider,
  get,
  useForm,
  useFormContext,
  useFormState,
  type Control,
  type FieldError,
  type FieldValues,
  type Path,
  type SubmitHandler,
  type UseFormProps,
  type UseFormReturn,
} from "react-hook-form";
import { useTranslation } from "react-i18next";
import type { z } from "zod";
import { Button, type ButtonProps } from "../components/Button";
import { cn } from "../lib/cn";

// Forms (design §5.2, §10.2): react-hook-form with a zod schema, errors in
// place under each field, a submitting state, and server error codes
// mapped onto the field they concern (or the whole form).

/**
 * useZodForm is useForm validated by a zod schema: values typed from the
 * schema's input, submits typed from its output; fields validate when
 * touched, then on every change.
 */
export function useZodForm<TIn extends FieldValues, TOut extends FieldValues = TIn>(
  schema: z.ZodType<TOut, TIn>,
  options?: Omit<UseFormProps<TIn, unknown, TOut>, "resolver">,
): UseFormReturn<TIn, unknown, TOut> {
  return useForm<TIn, unknown, TOut>({ mode: "onTouched", ...options, resolver: zodResolver(schema) });
}

export type FormProps<TIn extends FieldValues, TOut> = Omit<FormHTMLAttributes<HTMLFormElement>, "onSubmit"> & {
  form: UseFormReturn<TIn, unknown, TOut>;
  onSubmit: SubmitHandler<TOut>;
  children: ReactNode;
};

/**
 * Form provides the form to its fields (FormField name=…, FormError,
 * FormSubmit) and submits through the schema: onSubmit gets valid values.
 */
export function Form<TIn extends FieldValues, TOut = TIn>({ form, onSubmit, children, className, ...rest }: FormProps<TIn, TOut>) {
  return (
    <FormProvider {...form}>
      <form noValidate {...rest} onSubmit={form.handleSubmit(onSubmit)} className={cn("flex flex-col gap-4", className)}>
        {children}
      </form>
    </FormProvider>
  );
}

/** The props a field's control receives (id and ARIA wiring). */
export type FieldControlProps = {
  id: string;
  "aria-invalid"?: boolean;
  "aria-describedby"?: string;
  "aria-required"?: boolean;
};

export type FormFieldProps = {
  label?: ReactNode;
  /** The field's name in the surrounding <Form>: its error shows here. */
  name?: string;
  /** An explicit error, for fields outside a <Form>. */
  error?: string | FieldError;
  hint?: ReactNode;
  required?: boolean;
  /** Marks the label "选填". */
  optional?: boolean;
  /** Content at the right of the label (a "max" link, a counter). */
  extra?: ReactNode;
  id?: string;
  className?: string;
  /** The control: an element (it receives id and aria-*), or a render function. */
  children: ReactElement | ((control: FieldControlProps) => ReactNode);
};

function messageOf(e: string | FieldError | undefined): string | undefined {
  if (!e) return undefined;
  return typeof e === "string" ? e : e.message;
}

/**
 * FormField lays out a label, the control, and the error in place of the
 * hint; inside a <Form>, `name` finds the field's error by itself.
 */
export function FormField(props: FormFieldProps) {
  const ctx = useFormContext() as UseFormReturn | null;
  if (ctx && props.name) return <ConnectedField {...props} name={props.name} control={ctx.control} />;
  return <FieldShell {...props} message={messageOf(props.error)} />;
}

function ConnectedField({ control, ...props }: FormFieldProps & { name: string; control: Control }) {
  const { errors } = useFormState({ control, name: props.name });
  const own = get(errors, props.name) as FieldError | undefined;
  return <FieldShell {...props} message={messageOf(props.error) ?? own?.message} />;
}

function FieldShell({ label, hint, required, optional, extra, id, className, children, message }: FormFieldProps & { message?: string }) {
  const { t } = useTranslation();
  const auto = useId();
  const childId = isValidElement(children) ? (children.props as { id?: unknown }).id : undefined;
  const fieldId = id ?? (typeof childId === "string" ? childId : auto);
  const msgId = `${fieldId}-msg`;
  const control: FieldControlProps = {
    id: fieldId,
    "aria-invalid": message ? true : undefined,
    "aria-describedby": message || hint ? msgId : undefined,
    "aria-required": required || undefined,
  };
  const rendered = typeof children === "function" ? children(control) : isValidElement(children) ? cloneElement(children, control) : children;
  return (
    <div className={cn("flex flex-col gap-1.5", className)}>
      {(label || extra) && (
        <div className="flex items-center justify-between gap-2">
          {label && (
            <label htmlFor={fieldId} className="text-sm text-fg-2">
              {label}
              {required && (
                <span aria-hidden className="ml-0.5 text-danger">
                  *
                </span>
              )}
              {optional && <span className="ml-1 text-xs text-fg-3">({t("ui.form.optional")})</span>}
            </label>
          )}
          {extra && <div className="text-xs text-fg-3">{extra}</div>}
        </div>
      )}
      {rendered}
      {(message || hint) && (
        <p id={msgId} role={message ? "alert" : undefined} className={cn("text-xs", message ? "text-danger" : "text-fg-3")}>
          {message ?? hint}
        </p>
      )}
    </div>
  );
}

/** FormError shows the form-level error (a server error with no field). */
export function FormError({ className }: { className?: string }) {
  const ctx = useFormContext() as UseFormReturn | null;
  return ctx ? <RootError control={ctx.control} className={className} /> : null;
}

function RootError({ control, className }: { control: Control; className?: string }) {
  const { errors } = useFormState({ control });
  const root = errors.root as (FieldError & { server?: FieldError }) | undefined;
  const message = root?.server?.message ?? root?.message;
  if (!message) return null;
  return (
    <div role="alert" className={cn("flex items-start gap-2 rounded-2 border border-danger/40 bg-danger/10 p-3 text-sm text-danger", className)}>
      <CircleAlert size={16} className="mt-0.5 shrink-0" />
      <span className="min-w-0 break-words">{message}</span>
    </div>
  );
}

/** FormSubmit is the submit button: busy while the form submits. */
export function FormSubmit(props: Omit<ButtonProps, "type">) {
  const ctx = useFormContext() as UseFormReturn | null;
  return ctx ? <SubmitButton control={ctx.control} {...props} /> : <Button type="submit" {...props} />;
}

function SubmitButton({ control, loading, ...props }: Omit<ButtonProps, "type"> & { control: Control }) {
  const { isSubmitting } = useFormState({ control });
  return <Button type="submit" loading={loading || isSubmitting} {...props} />;
}

export type ServerErrorMapping = {
  /** The form field the error belongs to, if the map names one. */
  field?: string;
  /** The localized message (core errorText). */
  message: string;
  code?: string;
  traceId?: string;
};

/**
 * mapServerError maps an API error to a field by its stable code: fieldMap
 * is { code: field }, and a key ending in "*" matches a code prefix
 * ("AUTH_OTP_*": "code"). Without a match the error is form-level. The
 * message is the code's translation (core errorText).
 */
export function mapServerError(error: unknown, fieldMap: Record<string, string> = {}): ServerErrorMapping {
  const message = errorText(error);
  if (!(error instanceof ApiError)) return { message };
  let field = fieldMap[error.code];
  if (!field) {
    for (const [pattern, f] of Object.entries(fieldMap)) {
      if (pattern.endsWith("*") && error.code.startsWith(pattern.slice(0, -1))) {
        field = f;
        break;
      }
    }
  }
  return { field, message, code: error.code, traceId: error.traceId || undefined };
}

/**
 * setServerError puts a server error on the form: on its field (focused)
 * when the map knows the code, else as the form-level error (root.server).
 */
export function setServerError<T extends FieldValues>(
  form: Pick<UseFormReturn<T, unknown, unknown>, "setError">,
  error: unknown,
  fieldMap: Partial<Record<string, Path<T>>> = {},
): ServerErrorMapping {
  const m = mapServerError(error, fieldMap as Record<string, string>);
  if (m.field) form.setError(m.field as Path<T>, { type: "server", message: m.message }, { shouldFocus: true });
  else form.setError("root.server", { type: "server", message: m.message });
  return m;
}
