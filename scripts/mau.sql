select
  count(distinct user_id) as mau_posts,
  date_trunc('month', updated_at) as month
from posts
group by month
order by month DESC;

select
  count(distinct user_id) as mau_pgs,
  date_trunc('month', updated_at) as month
from projects
group by month
order by month DESC;
