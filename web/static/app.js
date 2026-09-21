const API_BASE = "/api";
let autoRefreshTimer = null;
let submissionMode = "single";

async function fetchStats() {
  const res = await fetch(`${API_BASE}/stats`);
  if (!res.ok) return;
  const stats = await res.json();
  document.getElementById("stat-total").textContent = stats.total_jobs ?? 0;
  document.getElementById("stat-pending").textContent = stats.pending_jobs ?? 0;
  document.getElementById("stat-processing").textContent = stats.processing_jobs ?? 0;
  document.getElementById("stat-completed").textContent = stats.completed_jobs ?? 0;
  document.getElementById("stat-failed").textContent = stats.failed_jobs ?? 0;
  document.getElementById("stat-workers").textContent =
    `${stats.active_workers ?? 0}/${stats.worker_count ?? 0}`;
}

async function fetchJobs() {
  const status = document.getElementById("status-filter").value;
  const url = status ? `${API_BASE}/jobs?status=${encodeURIComponent(status)}` : `${API_BASE}/jobs`;
  const res = await fetch(url);
  if (!res.ok) return;
  const jobs = await res.json();
  renderJobs(jobs);
}

function renderJobs(jobs) {
  const body = document.getElementById("jobs-body");
  const table = document.getElementById("jobs-table");
  const empty = document.getElementById("empty-state");
  body.innerHTML = "";

  if (!jobs || jobs.length === 0) {
    table.style.display = "none";
    empty.classList.remove("hidden");
    return;
  }
  table.style.display = "";
  empty.classList.add("hidden");

  for (const j of jobs) {
    const tr = document.createElement("tr");
    tr.innerHTML = `
      <td>#${j.id}</td>
      <td>${escapeHtml(j.type)}</td>
      <td><span class="badge ${j.status}">${j.status}</span></td>
      <td>${j.attempts}/${j.max_attempts}</td>
      <td>${formatTime(j.created_at)}</td>
      <td>${formatTime(j.updated_at)}</td>
      <td><button class="delete-btn" data-id="${j.id}">Delete</button></td>
    `;
    tr.addEventListener("click", (e) => {
      if (e.target.classList.contains("delete-btn")) return;
      showJobDetail(j);
    });
    tr.querySelector(".delete-btn").addEventListener("click", async (e) => {
      e.stopPropagation();
      await deleteJob(j.id);
      refreshAll();
    });
    body.appendChild(tr);
  }
}

function showJobDetail(job) {
  document.getElementById("modal-title").textContent = `${job.type} · #${job.id}`;
  document.getElementById("modal-body").textContent = JSON.stringify(job, null, 2);
  document.getElementById("job-modal").classList.remove("hidden");
}

async function deleteJob(id) {
  await fetch(`${API_BASE}/jobs/${id}`, { method: "DELETE" });
}

function formatTime(iso) {
  if (!iso) return "–";
  const d = new Date(iso);
  return d.toLocaleString();
}

function escapeHtml(s) {
  const div = document.createElement("div");
  div.textContent = s;
  return div.innerHTML;
}

async function refreshAll() {
  await Promise.all([fetchStats(), fetchJobs()]);
}

function setupAutoRefresh() {
  const checkbox = document.getElementById("auto-refresh");
  function apply() {
    if (autoRefreshTimer) clearInterval(autoRefreshTimer);
    if (checkbox.checked) {
      autoRefreshTimer = setInterval(refreshAll, 3000);
    }
  }
  checkbox.addEventListener("change", apply);
  apply();
}

function setSubmissionMode(mode) {
  submissionMode = mode;
  document.querySelectorAll(".mode-btn").forEach((button) => {
    button.classList.toggle("active", button.dataset.mode === mode);
  });

  const batchSizeField = document.querySelector(".batch-size-field");
  const submitButton = document.getElementById("submit-btn");
  batchSizeField.classList.toggle("hidden", mode !== "batch");
  submitButton.textContent = mode === "batch" ? "Submit Batch" : "Submit Job";
}

document.querySelectorAll(".mode-btn").forEach((button) => {
  button.addEventListener("click", () => setSubmissionMode(button.dataset.mode));
});

document.getElementById("job-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const type = document.getElementById("job-type").value;
  const maxAttempts = parseInt(document.getElementById("max-attempts").value, 10) || 3;
  const payloadText = document.getElementById("payload").value;
  const statusEl = document.getElementById("submit-status");

  let payload;
  try {
    payload = JSON.parse(payloadText);
  } catch (err) {
    statusEl.textContent = "Invalid JSON payload: " + err.message;
    statusEl.className = "submit-status err";
    return;
  }

  const batchSize = Math.min(
    100,
    Math.max(1, parseInt(document.getElementById("batch-size").value, 10) || 1)
  );

  try {
    const isBatch = submissionMode === "batch";
    const responseBody = isBatch
      ? {
          jobs: Array.from({ length: batchSize }, () => ({
            type,
            payload,
            max_attempts: maxAttempts,
          })),
        }
      : {
          type,
          payload,
          max_attempts: maxAttempts,
        };

    const res = await fetch(
      isBatch ? `${API_BASE}/jobs/batch` : `${API_BASE}/jobs`,
      {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(responseBody),
      }
    );

    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(err.error || `HTTP ${res.status}`);
    }

    const result = await res.json();
    statusEl.textContent = isBatch
      ? `${result.jobs.length} jobs submitted.`
      : `Job #${result.id} submitted.`;
    statusEl.className = "submit-status ok";
    refreshAll();
  } catch (err) {
    statusEl.textContent = "Failed to submit job: " + err.message;
    statusEl.className = "submit-status err";
  }
});

document.getElementById("status-filter").addEventListener("change", fetchJobs);
document.getElementById("refresh-btn").addEventListener("click", refreshAll);
document.getElementById("modal-close").addEventListener("click", () => {
  document.getElementById("job-modal").classList.add("hidden");
});
document.getElementById("job-modal").addEventListener("click", (e) => {
  if (e.target.id === "job-modal") e.target.classList.add("hidden");
});

refreshAll();
setupAutoRefresh();
