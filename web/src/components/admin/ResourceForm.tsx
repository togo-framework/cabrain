import { useEffect, useState } from "react";
import {
  Input, Textarea, Switch, Label,
  Select, SelectTrigger, SelectValue, SelectContent, SelectItem,
} from "@togo-framework/ui";
import {
  adminList, controlFor, relationTable, validateField, rowLabel,
  type ResourceField,
} from "../../lib/admin";

const labelOf = (name: string) => name.replace(/_id$/, "").replace(/_/g, " ").replace(/\b\w/g, (c) => c.toUpperCase());

type FormState = Record<string, string>;
type Errors = Record<string, string>;

/** A Filament-style schema form: every field renders the control its type implies
 * (text/textarea/number/switch/date/datetime/email/select-enum/relation/json),
 * with required+format validation and inline errors. Generic over any resource. */
export function ResourceForm({
  fields, value, errors, onChange, language = "en",
}: {
  fields: ResourceField[];
  value: FormState;
  errors: Errors;
  onChange: (next: FormState) => void;
  language?: string;
}) {
  const ar = language === "ar";
  const set = (name: string, v: string) => onChange({ ...value, [name]: v });

  return (
    <div className="grid gap-4 sm:grid-cols-2">
      {fields.map((f) => {
        const control = controlFor(f);
        const wide = control === "textarea" || control === "json";
        return (
          <div key={f.name} className={wide ? "sm:col-span-2" : ""}>
            <Field f={f} control={control} value={value[f.name] ?? ""} error={errors[f.name]} onChange={(v) => set(f.name, v)} language={language} ar={ar} />
          </div>
        );
      })}
    </div>
  );
}

function Field({ f, control, value, error, onChange, language, ar }: {
  f: ResourceField; control: string; value: string; error?: string; onChange: (v: string) => void; language: string; ar: boolean;
}) {
  const id = f.name;
  const req = !f.nullable && control !== "switch";
  const errText = error ? (ar
    ? { required: "هذا الحقل مطلوب", email: "بريد إلكتروني غير صالح", number: "رقم غير صالح" }[error] ?? error
    : { required: "This field is required", email: "Invalid email", number: "Invalid number" }[error] ?? error) : "";

  const lbl = (
    <Label htmlFor={id}>{labelOf(f.name)}{req && <span className="text-destructive"> *</span>}</Label>
  );
  const errEl = errText && <p className="text-xs text-destructive">{errText}</p>;

  if (control === "switch") {
    return (
      <div className="flex items-center justify-between gap-3 rounded-lg border border-border px-3 py-2.5">
        {lbl}
        <Switch checked={value === "true"} onCheckedChange={(c: boolean) => onChange(c ? "true" : "false")} />
      </div>
    );
  }

  if (control === "select" && f.enum?.length) {
    return (
      <div className="space-y-1.5">
        {lbl}
        <Select value={value} onValueChange={onChange}>
          <SelectTrigger id={id} aria-invalid={!!error}><SelectValue placeholder={ar ? "اختر…" : "Select…"} /></SelectTrigger>
          <SelectContent>{f.enum.map((o) => <SelectItem key={o} value={o} className="capitalize">{o}</SelectItem>)}</SelectContent>
        </Select>
        {errEl}
      </div>
    );
  }

  if (control === "relation") {
    return (
      <div className="space-y-1.5">
        {lbl}
        <RelationPicker f={f} value={value} onChange={onChange} ar={ar} invalid={!!error} />
        {errEl}
      </div>
    );
  }

  return (
    <div className="space-y-1.5">
      {lbl}
      {control === "textarea" || control === "json" ? (
        <Textarea id={id} rows={control === "json" ? 5 : 4} value={value} aria-invalid={!!error}
          className={control === "json" ? "font-mono text-xs" : ""}
          placeholder={control === "json" ? "{ }" : undefined}
          onChange={(e) => onChange(e.target.value)} />
      ) : (
        <Input id={id} aria-invalid={!!error}
          type={control === "number" ? "number" : control === "datetime" ? "datetime-local" : control === "date" ? "date" : control === "email" ? "email" : "text"}
          value={value} onChange={(e) => onChange(e.target.value)} />
      )}
      {errEl}
    </div>
  );
}

/** Belongs-to relation picker — fetches the related resource and lists its rows. */
function RelationPicker({ f, value, onChange, ar, invalid }: { f: ResourceField; value: string; onChange: (v: string) => void; ar: boolean; invalid: boolean }) {
  const table = relationTable(f)!;
  const [opts, setOpts] = useState<{ id: string; label: string }[] | null>(null);
  useEffect(() => {
    adminList(table).then((rows) => setOpts(rows.map((r) => ({ id: String(r.id), label: rowLabel(r) })))).catch(() => setOpts([]));
  }, [table]);
  return (
    <Select value={value} onValueChange={onChange}>
      <SelectTrigger aria-invalid={invalid}><SelectValue placeholder={opts === null ? (ar ? "جارٍ التحميل…" : "Loading…") : (ar ? `اختر ${table}` : `Select ${table}`)} /></SelectTrigger>
      <SelectContent>
        {(opts ?? []).map((o) => <SelectItem key={o.id} value={o.id}>{o.label}</SelectItem>)}
        {opts && opts.length === 0 && <div className="px-2 py-1.5 text-xs text-muted-foreground">{ar ? "لا توجد سجلات" : "No records"}</div>}
      </SelectContent>
    </Select>
  );
}

/** Validate the whole form against the schema; returns {errors, ok}. */
export function validateForm(fields: ResourceField[], value: FormState): { errors: Errors; ok: boolean } {
  const errors: Errors = {};
  for (const f of fields) {
    const e = validateField(f, value[f.name] ?? "");
    if (e) errors[f.name] = e;
  }
  return { errors, ok: Object.keys(errors).length === 0 };
}
