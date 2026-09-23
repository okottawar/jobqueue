const API_BASE = "/api";

function getAuthHeader() {
  const username = document.getElementById("auth-username").value;
  const password = document.getElementById("auth-password").value;
  if (!username || !password) {
    throw new Error("Enter the API username and password first.");
  }
  return "Basic " + btoa(`${username}:${password}`);
}
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
      try {
        await deleteJob(j.id);
        refreshAll();
      } catch (err) {
        alert(err.message);
      }
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
  const res = await fetch(`${API_BASE}/jobs/${id}`, {
    method: "DELETE",
    headers: { Authorization: getAuthHeader() },
  });
  if (res.status === 401) {
    throw new Error("Authentication failed.");
  }
  if (!res.ok) {
    throw new Error(`HTTP ${res.status}`);
  }
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

  const singleFields = document.getElementById("single-fields");
  const payloadLabel = document.getElementById("payload-label");
  const payload = document.getElementById("payload");
  const submitButton = document.getElementById("submit-btn");

  const isBatch = mode === "batch";
  singleFields.classList.toggle("hidden", isBatch);
  payloadLabel.textContent = isBatch ? "Batch Jobs (JSON array)" : "Payload (JSON)";
  submitButton.textContent = isBatch ? "Submit Batch" : "Submit Job";

  if (isBatch) {
    payload.value = JSON.stringify([
      {
        type: "email",
        payload: {
          to: "alice@example.com",
          subject: "Weekly account summary"
        },
        max_attempts: 3
      },
      {
        type: "report",
        payload: {},
        max_attempts: 3
      },
      {
        type: "webhook",
        payload: {
          url: "https://example.com/webhook"
        },
        max_attempts: 3
      }
    ], null, 2);
  } else {
    payload.value = '{"to":"user@example.com","subject":"Welcome"}';
  }
}

document.querySelectorAll(".mode-btn").forEach((button) => {
  button.addEventListener("click", () => setSubmissionMode(button.dataset.mode));
});

document.getElementById("job-form").addEventListener("submit", async (e) => {
  e.preventDefault();
  const statusEl = document.getElementById("submit-status");
  const payloadText = document.getElementById("payload").value;

  try {
    let responseBody;
    let endpoint;

    if (submissionMode === "batch") {
      let jobs;
      try {
        jobs = JSON.parse(payloadText);
      } catch (err) {
        throw new Error("Invalid batch JSON: " + err.message);
      }

      if (!Array.isArray(jobs)) {
        throw new Error("Batch payload must be a JSON array of jobs.");
      }
      if (jobs.length < 1 || jobs.length > 100) {
        throw new Error("Batch must contain between 1 and 100 jobs.");
      }

      for (let i = 0; i < jobs.length; i += 1) {
        const item = jobs[i];
        if (!item || typeof item !== "object" || Array.isArray(item)) {
          throw new Error(`Job ${i + 1} must be a JSON object.`);
        }
        if (typeof item.type !== "string" || !item.type.trim()) {
          throw new Error(`Job ${i + 1} is missing a type.`);
        }
        if (!Object.prototype.hasOwnProperty.call(item, "payload")) {
          throw new Error(`Job ${i + 1} is missing a payload.`);
        }
        if (item.max_attempts !== undefined &&
            (!Number.isInteger(item.max_attempts) || item.max_attempts < 0)) {
          throw new Error(`Job ${i + 1} has an invalid max_attempts value.`);
        }
      }

      endpoint = `${API_BASE}/jobs/batch`;
      responseBody = { jobs };
    } else {
      const type = document.getElementById("job-type").value;
      const maxAttempts = parseInt(document.getElementById("max-attempts").value, 10) || 3;

      let payload;
      try {
        payload = JSON.parse(payloadText);
      } catch (err) {
        throw new Error("Invalid JSON payload: " + err.message);
      }

      endpoint = `${API_BASE}/jobs`;
      responseBody = { type, payload, max_attempts: maxAttempts };
    }

    const authHeader = getAuthHeader();
    const res = await fetch(endpoint, {
      method: "POST",
      headers: {
        "Content-Type": "application/json",
        Authorization: authHeader,
      },
      body: JSON.stringify(responseBody),
    });

    if (!res.ok) {
      const err = await res.json().catch(() => ({}));
      throw new Error(err.error || `HTTP ${res.status}`);
    }

    const result = await res.json();
    statusEl.textContent = submissionMode === "batch"
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
