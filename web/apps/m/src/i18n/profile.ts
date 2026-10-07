// Strings of the profile page on the phone: the avatar and the username
// (namespace mProfile; design 2026-10-07, avatars and usernames). The error
// codes' messages are shared (errors.USER_USERNAME_*, errors.USER_AVATAR_*).
import zhTW from "./profile.zh-TW";

export default {
  "zh-CN": {
    mProfile: {
      title: "个人资料",
      avatar: {
        change: "更换头像",
        remove: "恢复默认",
        hint: "PNG、JPEG 或 WebP 图片，取中间的正方形",
        builtIn: "现在是系统默认头像",
        removeTitle: "恢复默认头像？",
        removeDesc: "上传的头像会被删除，改用系统默认头像，之后可以随时重新上传。",
        removed: "已恢复默认头像",
        uploaded: "头像已更新",
        preparing: "正在处理图片…",
        uploading: "正在上传",
        saving: "正在保存…",
        cancel: "取消上传",
        problems: {
          type: "只支持 PNG、JPEG、WebP 格式的图片",
          large: "图片太大，请选择 20 MB 以内的图片",
          small: "图片太小：宽和高都至少 64 像素",
          decode: "无法读取这张图片，请换一张",
        },
      },
      username: {
        title: "用户名",
        cooldown: "{{time}} 后可再次修改",
        sheetTitle: "修改用户名",
        label: "新用户名",
        placeholder: "例如 satoshi_n",
        hint: "3–20 个字母、数字或下划线，不能以下划线开头；修改后 7 天内不能再改",
        save: "保存",
        saved: "用户名已修改",
        problems: {
          chars: "只能包含字母、数字和下划线",
          start: "不能以下划线开头",
          length: "长度为 3–20 个字符",
          reserved: "这是保留名称，请换一个",
          same: "与现在的用户名相同",
        },
      },
      uidCopied: "UID 已复制",
      joined: "注册时间",
      locked: "账户已冻结，暂时不能修改资料",
      note: "头像和用户名会显示在你的账户里，登录仍使用邮箱或手机号。",
    },
  },
  en: {
    mProfile: {
      title: "Profile",
      avatar: {
        change: "Change avatar",
        remove: "Use default",
        hint: "A PNG, JPEG or WebP image, its middle square kept",
        builtIn: "You have the built-in avatar",
        removeTitle: "Go back to the default avatar?",
        removeDesc: "The uploaded avatar is deleted and the built-in one used; you can upload another at any time.",
        removed: "Back to the default avatar",
        uploaded: "Avatar updated",
        preparing: "Processing the picture…",
        uploading: "Uploading",
        saving: "Saving…",
        cancel: "Cancel upload",
        problems: {
          type: "Only PNG, JPEG and WebP pictures",
          large: "The picture is too large: choose one under 20 MB",
          small: "The picture is too small: at least 64 pixels each way",
          decode: "That picture cannot be read: choose another",
        },
      },
      username: {
        title: "Username",
        cooldown: "May change again after {{time}}",
        sheetTitle: "Change username",
        label: "New username",
        placeholder: "e.g. satoshi_n",
        hint: "3 to 20 letters, digits or underscores, not starting with an underscore; it stays 7 days once changed",
        save: "Save",
        saved: "Username changed",
        problems: {
          chars: "Letters, digits and underscores only",
          start: "It may not start with an underscore",
          length: "3 to 20 characters",
          reserved: "That name is reserved: choose another",
          same: "That is your username now",
        },
      },
      uidCopied: "UID copied",
      joined: "Joined",
      locked: "The account is frozen: the profile cannot change for now",
      note: "Your avatar and username show across your account; you still sign in with your email or phone.",
    },
  },
  // Generated from "zh-CN" (core's scripts/gen-zh-tw.mjs).
  "zh-TW": zhTW,
};
