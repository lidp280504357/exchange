import { errorText, selectUserId, useSession } from "@exchange/core";
import { AVATAR_TYPES, AvatarProblemError, deleteAvatar, useAvatarUpload, type AvatarUpload } from "@exchange/core/user/avatar";
import { keepProfile, type Profile } from "@exchange/core/user/profile";
import { Avatar, Button, listItem, toast } from "@exchange/ui";
import { MyAvatar } from "@exchange/ui/profile/MyAvatar";
import { UploadRing } from "@exchange/ui/profile/UploadRing";
import { useQueryClient } from "@tanstack/react-query";
import { Camera } from "lucide-react";
import { motion } from "motion/react";
import { useRef, useState } from "react";
import { useTranslation } from "react-i18next";
import { ConfirmSheet } from "../parts/ConfirmSheet";

const SIZE = 96;

/**
 * AvatarEditor changes the avatar on the phone (design 2026-10-07, avatars
 * and usernames §1 #2): tapping the avatar or "change" opens the photo
 * picker; the picture is shrunk on the phone and uploaded, its preview in a
 * ring of the progress; "use default" deletes the uploaded one after a
 * confirmation sheet.
 */
export function AvatarEditor({ profile, locked }: { profile: Profile | undefined; locked: boolean }) {
  const { t } = useTranslation();
  const qc = useQueryClient();
  const userId = useSession(selectUserId);
  const { state, upload, cancel } = useAvatarUpload();
  const input = useRef<HTMLInputElement>(null);
  const [confirm, setConfirm] = useState(false);
  const [removing, setRemoving] = useState(false);
  const busy = state.phase === "preparing" || state.phase === "uploading";
  // Once the picture is up the server is saving it: cancelling would not stop that (review GJ, F9).
  const saving = state.phase === "uploading" && state.progress >= 1;
  const uploaded = Boolean(profile?.avatar_url);
  const usable = Boolean(profile) && !locked;
  const pick = () => input.current?.click();

  const choose = async (file: File | undefined) => {
    if (!file || !usable) return;
    if (await upload(file)) toast.success(t("mProfile.avatar.uploaded"));
  };

  const remove = async () => {
    setRemoving(true);
    try {
      keepProfile(qc, await deleteAvatar());
      setConfirm(false);
      toast.success(t("mProfile.avatar.removed"));
    } catch (e) {
      toast.error(errorText(e));
    } finally {
      setRemoving(false);
    }
  };

  return (
    <motion.section
      variants={listItem}
      initial="initial"
      animate="animate"
      custom={0}
      data-testid="profile-avatar"
      className="flex flex-col items-center gap-3 rounded-3 bg-bg-1 px-4 pb-5 pt-6"
    >
      <button
        type="button"
        onClick={pick}
        disabled={!usable || busy}
        aria-label={t("mProfile.avatar.change")}
        className="relative shrink-0 rounded-full"
        style={{ width: SIZE, height: SIZE }}
      >
        {state.phase === "uploading" ? <Avatar src={state.preview} seed={userId} size={SIZE} /> : <MyAvatar size={SIZE} />}
        <UploadRing state={state} />
        {usable && !busy && (
          <span aria-hidden className="absolute -bottom-0.5 -right-0.5 grid size-8 place-items-center rounded-full border-2 border-bg-1 bg-brand text-brand-fg">
            <Camera size={15} />
          </span>
        )}
      </button>
      <Status state={state} uploaded={uploaded} />
      <div className="flex w-full gap-2">
        {busy ? (
          !saving && (
            <Button variant="secondary" size="lg" block onClick={cancel}>
              {t("mProfile.avatar.cancel")}
            </Button>
          )
        ) : (
          <>
            <Button variant="secondary" size="lg" block disabled={!usable} onClick={pick}>
              {t("mProfile.avatar.change")}
            </Button>
            {uploaded && (
              <Button variant="ghost" size="lg" block disabled={!usable} onClick={() => setConfirm(true)}>
                {t("mProfile.avatar.remove")}
              </Button>
            )}
          </>
        )}
      </div>
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
      <ConfirmSheet
        open={confirm}
        onOpenChange={setConfirm}
        title={t("mProfile.avatar.removeTitle")}
        description={t("mProfile.avatar.removeDesc")}
        tone="danger"
        confirmText={t("mProfile.avatar.remove")}
        loading={removing}
        onConfirm={() => void remove()}
      />
    </motion.section>
  );
}

// Status says what the upload is doing, why it failed, or what the rules are.
function Status({ state, uploaded }: { state: AvatarUpload; uploaded: boolean }) {
  const { t } = useTranslation();
  if (state.phase === "failed") {
    const message = state.error instanceof AvatarProblemError ? t(`mProfile.avatar.problems.${state.error.problem}`) : errorText(state.error);
    return (
      <p role="alert" className="text-center text-sm text-danger">
        {message}
      </p>
    );
  }
  if (state.phase === "preparing" || state.phase === "uploading") {
    const text =
      state.phase === "preparing"
        ? t("mProfile.avatar.preparing")
        : state.progress < 1
          ? `${t("mProfile.avatar.uploading")} ${Math.round(state.progress * 100)}%`
          : t("mProfile.avatar.saving");
    return (
      <p aria-live="polite" className="text-center text-sm text-fg-2 tabular-nums">
        {text}
      </p>
    );
  }
  return (
    <p className="text-center text-xs leading-relaxed text-fg-3">
      {t("mProfile.avatar.hint")}
      {!uploaded && (
        <>
          <br />
          {t("mProfile.avatar.builtIn")}
        </>
      )}
    </p>
  );
}
