/**
 * Copyright (c) 2026, WSO2 LLC. (https://www.wso2.com).
 *
 * WSO2 LLC. licenses this file to you under the Apache License,
 * Version 2.0 (the "License"); you may not use this file except
 * in compliance with the License.
 * You may obtain a copy of the License at
 *
 * http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing,
 * software distributed under the License is distributed on an
 * "AS IS" BASIS, WITHOUT WARRANTIES OR CONDITIONS OF ANY
 * KIND, either express or implied.  See the License for the
 * specific language governing permissions and limitations
 * under the License.
 */

// Collab question FORM (2026-07-23 spike): EVERY agent question — one or many —
// is answered here. The chat panel only points at it and the spec body is taken
// over by this full-panel form, so questions always appear in one consistent
// place instead of splitting between two surfaces.
//
// The form is driven by the room's shared Yjs `questions` map, so EVERY collab
// participant sees the questions and each other's selections live. Only the
// user who triggered the turn (`ownerId`) can submit or skip — they hold the
// SSE stream back to the agent; everyone else co-authors.

import { Box, Button, Checkbox, Chip, Stack, TextField, Tooltip, Typography } from "@wso2/oxygen-ui";
import { Sparkles, Users } from "@wso2/oxygen-ui-icons-react";
import type { Doc } from "yjs";
import type { AskQuestionInput, QuestionAnswer } from "@aep/agent-stream";
import { serializeQuestionAnswer } from "../../agent-chat/questionCards";
import { chatKeyFor, setPendingSeed } from "../../agent-chat/chatStore";
import { useCurrentAuthor } from "../../agent-chat/currentUser";
import {
  closeRoomQuestion,
  setRoomAnswer,
  type RoomQuestion,
} from "../../agent-chat/questionRoom";

/** One question block: a heading, its options, and an "Other…" free-text row. */
function QuestionBlock({
  q,
  answer,
  disabled,
  onSelect,
  onNote,
}: {
  q: AskQuestionInput;
  answer: QuestionAnswer | undefined;
  disabled: boolean;
  onSelect: (label: string) => void;
  onNote: (note: string) => void;
}) {
  const multi = q.multiSelect === true;
  const selected = answer?.selected ?? [];
  return (
    <Box sx={{ mb: 4 }}>
      <Typography variant="subtitle1" sx={{ fontWeight: 600 }}>
        {q.question}
      </Typography>
      {multi && (
        <Typography variant="body2" color="text.secondary" sx={{ mb: 1 }}>
          Pick as many as apply
        </Typography>
      )}
      <Stack direction="row" spacing={1} useFlexGap sx={{ flexWrap: "wrap", mt: 1 }}>
        {q.options.map((opt) => {
          const isOn = selected.includes(opt.label);
          const chip = (
            <Chip
              key={opt.label}
              label={
                multi ? (
                  <Stack direction="row" spacing={0.5} sx={{ alignItems: "center" }}>
                    <Checkbox size="small" checked={isOn} sx={{ p: 0, mr: 0.5 }} disableRipple />
                    <span>{opt.label}</span>
                  </Stack>
                ) : (
                  opt.label
                )
              }
              onClick={disabled ? undefined : () => onSelect(opt.label)}
              variant={isOn ? "filled" : "outlined"}
              color={isOn ? "primary" : opt.recommended ? "primary" : "default"}
              disabled={disabled}
              sx={{ borderRadius: 8, py: 2, px: 0.5, cursor: disabled ? "default" : "pointer" }}
            />
          );
          return opt.description ? (
            <Tooltip key={opt.label} title={opt.description}>
              <span>{chip}</span>
            </Tooltip>
          ) : (
            chip
          );
        })}
        <TextField
          size="small"
          placeholder="Other…"
          value={answer?.freeText ?? ""}
          disabled={disabled}
          onChange={(e) => onNote(e.target.value)}
          sx={{ minWidth: 200, "& .MuiOutlinedInput-root": { borderRadius: 8 } }}
        />
      </Stack>
    </Box>
  );
}

export function SpecQuestionForm({
  doc,
  entry,
  org,
  projectName,
}: {
  /** The live room Y.Doc — selections write straight into its shared map. */
  doc: Doc;
  /** The pending question entry from the room (one question or many). */
  entry: RoomQuestion;
  org: string;
  projectName: string;
}) {
  const me = useCurrentAuthor();
  const isOwner = entry.ownerId === me.id;
  const answers: QuestionAnswer[] =
    entry.answers ?? entry.questions.map(() => ({ selected: [] }));

  const write = (next: QuestionAnswer[]) => setRoomAnswer(doc, entry.toolCallId, next);

  const select = (qi: number, label: string) => {
    const multi = entry.questions[qi]!.multiSelect === true;
    write(
      answers.map((a, i) => {
        if (i !== qi) return a;
        const has = a.selected.includes(label);
        const selected = multi
          ? has
            ? a.selected.filter((l) => l !== label)
            : [...a.selected, label]
          : has
            ? []
            : [label];
        return { ...a, selected };
      }),
    );
  };

  const note = (qi: number, freeText: string) => {
    write(answers.map((a, i) => (i === qi ? { ...a, freeText } : a)));
  };

  const allAnswered = answers.every(
    (a) => a.selected.length > 0 || (a.freeText ?? "").trim().length > 0,
  );
  const canSubmit = isOwner && allAnswered;

  const submit = () => {
    if (!canSubmit) return;
    const cleaned = answers.map((a) => ({
      selected: a.selected,
      ...(a.freeText?.trim() ? { freeText: a.freeText.trim() } : {}),
    }));
    setPendingSeed(chatKeyFor(org, projectName), serializeQuestionAnswer(entry.questions, cleaned));
    closeRoomQuestion(doc, entry.toolCallId);
  };

  // Skip: close the form for the WHOLE room and tell the agent to stop asking
  // and proceed — otherwise the turn sits waiting on an answer that isn't
  // coming. Gated to the asker, same as submit (only they can drive the agent).
  const skip = () => {
    if (!isOwner) return;
    setPendingSeed(
      chatKeyFor(org, projectName),
      "Skip these questions — stop interviewing and proceed with your best assumptions, stating them.",
    );
    closeRoomQuestion(doc, entry.toolCallId);
  };

  return (
    <Box
      data-testid="spec-question-form"
      sx={{ flexGrow: 1, minWidth: 0, minHeight: 0, display: "flex", flexDirection: "column" }}
    >
      {/* Scrollable question list — a long interview scrolls here, the footer stays put. */}
      <Box sx={{ flexGrow: 1, overflowY: "auto", px: 5, py: 4 }}>
        <Box sx={{ maxWidth: 820, mx: "auto" }}>
          <Stack direction="row" spacing={1} sx={{ alignItems: "center", mb: 0.5 }}>
            <Sparkles size={18} color="var(--oxygen-palette-primary-main, currentColor)" />
            <Typography variant="h5" sx={{ fontWeight: 700 }}>
              Quick questions
            </Typography>
          </Stack>
          <Stack direction="row" spacing={0.75} sx={{ alignItems: "center", mb: 4 }}>
            <Users size={14} />
            <Typography variant="body2" color="text.secondary">
              {isOwner
                ? "Everyone on this project can answer together — you'll send the answers."
                : `Everyone on this project can answer together — ${entry.ownerId} sends them.`}
            </Typography>
          </Stack>

          {entry.questions.map((q, qi) => (
            <QuestionBlock
              key={qi}
              q={q}
              answer={answers[qi]}
              disabled={false}
              onSelect={(label) => select(qi, label)}
              onNote={(text) => note(qi, text)}
            />
          ))}
        </Box>
      </Box>

      {/* Sticky footer — Continue sits bottom-right, gated to the asker. */}
      <Stack
        direction="row"
        spacing={2}
        sx={{
          alignItems: "center",
          justifyContent: "flex-end",
          px: 5,
          py: 2,
          borderTop: 1,
          borderColor: "divider",
          flexShrink: 0,
        }}
      >
        {!isOwner && (
          <Typography variant="caption" color="text.secondary">
            Your picks are shared live — only the person who asked can send them.
          </Typography>
        )}
        <Button variant="text" color="inherit" disabled={!isOwner} onClick={skip}>
          Skip questions
        </Button>
        <Button variant="contained" disabled={!canSubmit} onClick={submit}>
          Continue
        </Button>
      </Stack>
    </Box>
  );
}
