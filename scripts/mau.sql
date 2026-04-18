select
  count(distinct user_id) as active_posts,
  date_trunc('month', updated_at) as month
from posts
group by month
order by month DESC;

select
  count(distinct user_id) as active_sites,
  date_trunc('month', updated_at) as month
from projects
group by month
order by month DESC;

select
  count(id) as new_users,
  date_trunc('month', created_at) as month
from app_users
group by month
order by month DESC;

select
  count(id) as subs,
  date_trunc('month', created_at) as month
from feature_flags
where name = 'plus' and expires_at > now()
group by month
order by month DESC;

select
	count(*) as total_subs
from feature_flags
where name = 'plus' and expires_at > now();
