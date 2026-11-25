import { useCallback, useState } from "react";
import { useSearchParams } from "react-router-dom";
import { useI18n } from "../lib/i18n";
import { ConversationList } from "./ConversationList";
import { Thread } from "./ConversationThread";
import "./conversations.css";

/** The narrow web window's page: the chat view there has no list of its own,
 *  so every conversation lives here. The desktop has them in the chat page. */
export default function ConversationsView() {
  const { t } = useI18n();
  const [params, setParams] = useSearchParams();
  const openId = params.get("open") ?? "";
  const [stamp, setStamp] = useState(0);
  const open = (id: string) => {
    const next = new URLSearchParams(params);
    id ? next.set("open", id) : next.delete("open");
    setParams(next, { replace: true });
  };
  const changed = useCallback(() => setStamp((n) => n + 1), []);
  return (
    <div className={`cv2${openId ? " has-open" : ""}`} data-testid="conversations">
      <aside className="cv2-list">
        <ConversationList openId={openId} refresh={stamp} onOpen={(c) => open(c.id)} onDeleted={(c) => { if (openId === c.id) open(""); }} />
      </aside>
      <section className="cv2-thread">
        {openId ? (
          <Thread key={openId} id={openId} onBack={() => open("")} onOpen={open} onChanged={changed} />
        ) : (
          <div className="cv2-none">{t("Pick a conversation")}</div>
        )}
      </section>
    </div>
  );
}
