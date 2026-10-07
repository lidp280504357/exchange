import { errorText, selectUserId, useSession } from "@exchange/core";
import { AVATAR_TYPES, AvatarProblemError, deleteAvatar, useAvatarUpload, type AvatarUpload } from "@exchange/core/user/avatar";
import { keepProfile, type Profile } from "@exchange/core/user/profile";
import { Avatar, Button, Dialog, listItem, toast, cn } from "@exchange/ui";
import { MyAvatar } from "@exchange/ui/profile/MyAvatar";
import { UploadRing } from "@exchange/ui/profile/UploadRing";
import { useQueryClient } from "@tanstack/react-query";
import { Camera } from "lucide-react";
import { motion } from "motion/react";
import { useRef, useState, type DragEvent } from "react";
import { useTranslation } from "react-i18next";

const SIZE = 88;

/**
 * AvatarCard changes the avatar (design 2026-10-07, avatars and usernames
 * §1 #2): a picture chosen or dropped on the card is shrunk in the browser
 * and uploaded, its preview in a ring of the progress; "use default"
 * deletes the uploaded one after a confirmation.
 */
export function AvatarCard({ profile, locked, index }: { profile: Profile | undefined; locked: boolean; index: number }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const userId = useSession(selectUserId);
  const { state, upload, cancel } = useAvatarUpload();
  const input = useRef<HTMLInputElement>(null);
  const [confirm, setConfirm] = useState(false);
  const [removing, setRemoving] = useState(false);
  const [over, setOver] = useState(false);
  const busy = state.phase === "preparing" || state.phase === "uploading";
  const uploaded = Boolean(profile?.avatar_url);
  const usable = Boolean(profile) && !locked;

  const choose = async (file: File | undefined) => {
    if (!file || !usable) return;
    if (await upload(file)) toast.success(t("pcProfile.avatar.uploaded"));
  };

  const remove = async () => {
    setRemoving(true);
    try {
      keepProfile(qc, await deleteAvatar());
      setConfirm(false);
      toast.success(t("pcProfile.avatar.removed"));
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setRemoving(false);
    }
  };

  const dragging = (e: DragEvent) => usable && !busy && e.dataTransfer.types.includes("Files");

  return (
    <motion.div
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={index}
      data-testid="profile-avatar"
      onDragOver={(e) => {
        if (!dragging(e)) return;
        e.preventDefault();
        setOver(true);
      }}
      onDragLeave={() => setOver(false)}
      onDrop={(e) => {
        e.preventDefault();
        setOver(false);
        if (usable && !busy) void choose(e.dataTransfer.files[0]);
      }}
      className={cn(
        "flex items-center gap-5 rounded-3 border bg-bg-1 p-5 transition-colors duration-[var(--t-base)]",
        over ? "border-brand bg-brand-soft" : "border-line-1",
      )}
    >
      <div className="relative shrink-0" style={{ width: SIZE, height: SIZE }}>
        {state.phase === "uploading" ? <Avatar src={state.preview} seed={userId} size={SIZE} /> : <MyAvatar size={SIZE} />}
        <UploadRing state={state} />
      </div>
      <div className="min-w-0 flex-1">
        <div className="font-medium text-fg-1">{t("pcProfile.avatar.title")}</div>
        <p className="mt-0.5 text-sm leading-relaxed text-fg-3">{over ? t("pcProfile.avatar.drop") : t("pcProfile.avatar.desc")}</p>
        <Status state={state} uploaded={uploaded} />
      </div>
      <div className="flex shrink-0 items-center gap-2">
        {busy ? (
          <Button size="sm" variant="ghost" onClick={cancel}>
            {t("pcProfile.avatar.cancel")}
          </Button>
        ) : (
          <>
            {uploaded && (
              <Button size="sm" variant="ghost" disabled={!usable} onClick={() => setConfirm(true)}>
                {t("pcProfile.avatar.remove")}
              </Button>
            )}
            <Button size="sm" variant="secondary" icon={<Camera size={14} />} disabled={!usable} onClick={() => input.current?.click()}>
              {t("pcProfile.avatar.change")}
            </Button>
          </>
        )}
        <input
          ref={input}
          type="file"
          accept={AVATAR_TYPES.join(",")}
          tabIndex={-1}
          aria-hidden
          data-testid="avatar-input"
          className="sr-only"
          onChange={(e) => {
            const file = e.currentTarget.files?.[0];
            // The same picture chosen again is a change too.
            e.currentTarget.value = "";
            void choose(file);
          }}
        />
      </div>
      <Dialog
        open={confirm}
        onOpenChange={setConfirm}
        title={t("pcProfile.avatar.removeTitle")}
        description={t("pcProfile.avatar.removeDesc")}
        size="sm"
        confirmVariant="danger"
        confirmText={t("pcProfile.avatar.remove")}
        confirmLoading={removing}
        onConfirm={() => void remove()}
      />
    </motion.div>
  );
}

// Status says what the upload is doing, or why it failed.
function Status({ state, uploaded }: { state: AvatarUpload; uploaded: boolean }) {
  const { t } = useTranslation();
  if (state.phase === "failed") {
    const message = state.error instanceof AvatarProblemError ? t(`pcProfile.avatar.problems.${state.error.problem}`) : errorText(state.error);
    return (
      <p role="alert" className="mt-2 text-sm text-danger">
        {message}
      </p>
    );
  }
  if (state.phase === "preparing" || state.phase === "uploading") {
    const text =
      state.phase === "preparing"
        ? t("pcProfile.avatar.preparing")
        : state.progress < 1
          ? `${t("pcProfile.avatar.uploading")} ${Math.round(state.progress * 100)}%`
          : t("pcProfile.avatar.saving");
    return (
      <p aria-live="polite" className="mt-2 text-sm text-fg-2 tabular-nums">
        {text}
      </p>
    );
  }
  return uploaded ? null : <p className="mt-2 text-xs text-fg-3">{t("pcProfile.avatar.builtIn")}</p>;
}
